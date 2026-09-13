package toolsdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	defaultHTTPFetchBodyBytes = 1 << 20
	maxHTTPFetchBodyBytes     = 4 << 20
)

type WorkspaceHTTPFetchArgs struct {
	Workspace       string            `json:"workspace"`
	URL             string            `json:"url"`
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Host            string            `json:"host,omitempty"`
	IdentityFile    string            `json:"identity_file,omitempty"`
	FollowRedirects *bool             `json:"follow_redirects,omitempty"`
	TimeoutMs       *int              `json:"timeout_ms,omitempty"`
	MaxBodyBytes    int               `json:"max_body_bytes,omitempty"`
}

type WorkspaceHTTPFetchResult struct {
	Status       int                 `json:"status"`
	EffectiveURL string              `json:"effective_url"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Body         string              `json:"body,omitempty"`
	Encoding     string              `json:"encoding"`
	BodyBytes    int                 `json:"body_bytes"`
}

var WorkspaceHTTPFetch = Tool[WorkspaceHTTPFetchArgs, WorkspaceHTTPFetchResult]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceHTTPFetch,
		Description: `Fetch an HTTP or HTTPS URL from the workspace or an optional SSH alias returned by remote_hosts. Only GET and HEAD are supported. Response bodies are bounded; binary bodies are returned as base64.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"url":       map[string]any{"type": "string", "description": "HTTP or HTTPS URL to fetch."},
				"method":    map[string]any{"type": "string", "description": "HTTP method. Defaults to GET.", "enum": []string{"GET", "HEAD"}},
				"headers":   map[string]any{"type": "object", "description": "Optional request headers. Header values are redacted from stored MCP activity metadata.", "additionalProperties": map[string]any{"type": "string"}},
				"host":      map[string]any{"type": "string", "description": "Optional SSH alias returned by remote_hosts from which to perform the request."},
				"follow_redirects": map[string]any{
					"type":        "boolean",
					"description": "Follow HTTP redirects. Defaults to true.",
					"default":     true,
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Optional per-call timeout in milliseconds. Omit to use the deployment-wide MCP tool timeout.",
					"minimum":     1,
				},
				"max_body_bytes": map[string]any{
					"type":        "integer",
					"description": "Maximum response body bytes. Defaults to 1 MiB, maximum 4 MiB.",
					"minimum":     1,
					"maximum":     maxHTTPFetchBodyBytes,
				},
			},
			Required: []string{"workspace", "url"},
		},
	},
	MCPAnnotations:     mcpReadOnlyOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceHTTPFetchArgs) (WorkspaceHTTPFetchResult, error) {
		if err := validateRemoteTarget(args.Host, args.IdentityFile); err != nil {
			return WorkspaceHTTPFetchResult{}, err
		}
		parsedURL, err := url.Parse(args.URL)
		if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return WorkspaceHTTPFetchResult{}, xerrors.New("url must be an absolute http or https URL")
		}
		method := strings.ToUpper(strings.TrimSpace(args.Method))
		if method == "" {
			method = "GET"
		}
		if method != "GET" && method != "HEAD" {
			return WorkspaceHTTPFetchResult{}, xerrors.New("method must be GET or HEAD")
		}
		for name, value := range args.Headers {
			if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n:") {
				return WorkspaceHTTPFetchResult{}, xerrors.Errorf("invalid HTTP header name %q", name)
			}
			if strings.ContainsAny(value, "\r\n") {
				return WorkspaceHTTPFetchResult{}, xerrors.Errorf("HTTP header %q contains a newline", name)
			}
		}
		maxBody := args.MaxBodyBytes
		if maxBody == 0 {
			maxBody = defaultHTTPFetchBodyBytes
		}
		if maxBody < 1 || maxBody > maxHTTPFetchBodyBytes {
			return WorkspaceHTTPFetchResult{}, xerrors.Errorf("max_body_bytes must be between 1 and %d", maxHTTPFetchBodyBytes)
		}

		operationCtx := ctx
		cancel := func() {}
		if args.TimeoutMs != nil {
			if *args.TimeoutMs <= 0 {
				return WorkspaceHTTPFetchResult{}, xerrors.New("timeout_ms must be greater than zero")
			}
			timeout := time.Duration(*args.TimeoutMs) * time.Millisecond
			if timeout > deps.MCPToolTimeoutMax() {
				return WorkspaceHTTPFetchResult{}, xerrors.Errorf("timeout_ms cannot exceed %d", deps.MCPToolTimeoutMax().Milliseconds())
			}
			operationCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		defer cancel()

		conn, err := openAgentConn(operationCtx, deps, args.Workspace)
		if err != nil {
			return WorkspaceHTTPFetchResult{}, err
		}
		defer conn.Close()

		followRedirects := true
		if args.FollowRedirects != nil {
			followRedirects = *args.FollowRedirects
		}
		command := buildHTTPFetchCommand(args, method, maxBody, followRedirects)
		output, err := runHelperCommand(operationCtx, conn, workspacesdk.RunCommandRequest{
			Host:         args.Host,
			IdentityFile: args.IdentityFile,
			Command:      command,
		})
		if err != nil {
			return WorkspaceHTTPFetchResult{}, xerrors.Errorf("http fetch: %w", err)
		}
		return parseHTTPFetchOutput(output, maxBody)
	},
}

//nolint:revive // followRedirects is a direct HTTP semantic option from the tool schema.
func buildHTTPFetchCommand(args WorkspaceHTTPFetchArgs, method string, maxBody int, followRedirects bool) string {
	curlArgs := []string{"curl", "--silent", "--show-error", "--proto", "=http,https", "--proto-redir", "=http,https"}
	if followRedirects {
		curlArgs = append(curlArgs, "--location")
	}
	if method == "HEAD" {
		curlArgs = append(curlArgs, "--head")
	}
	keys := make([]string, 0, len(args.Headers))
	for key := range args.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		curlArgs = append(curlArgs, "--header", key+": "+args.Headers[key])
	}
	curlArgs = append(curlArgs, "--dump-header", `"$tmp/headers"`)
	if method == "HEAD" {
		curlArgs = append(curlArgs, "--output", "/dev/null")
	} else {
		curlArgs = append(curlArgs, "--max-filesize", strconv.Itoa(maxBody), "--output", `"$tmp/body"`)
	}
	curlArgs = append(curlArgs, "--write-out", "%{http_code}\\n%{url_effective}", "--url", args.URL)

	quotedArgs := make([]string, 0, len(curlArgs))
	for _, arg := range curlArgs {
		if strings.HasPrefix(arg, `"$tmp/`) {
			quotedArgs = append(quotedArgs, arg)
		} else {
			quotedArgs = append(quotedArgs, toolShellQuote(arg))
		}
	}
	return "set -eu; tmp=$(mktemp -d); cleanup(){ rm -rf -- \"$tmp\"; }; trap cleanup EXIT HUP INT TERM; " +
		": > \"$tmp/body\"; : > \"$tmp/headers\"; " + strings.Join(quotedArgs, " ") + " > \"$tmp/meta\"; " +
		"body_size=$(wc -c < \"$tmp/body\"); if [ \"$body_size\" -gt " + strconv.Itoa(maxBody) + " ]; then echo 'response body exceeds max_body_bytes' >&2; exit 63; fi; " +
		"meta_size=$(wc -c < \"$tmp/meta\"); headers_size=$(wc -c < \"$tmp/headers\"); " +
		"printf 'CODER_HTTP_FETCH_V1\\n%s\\n%s\\n%s\\n' \"$meta_size\" \"$headers_size\" \"$body_size\"; cat \"$tmp/meta\" \"$tmp/headers\" \"$tmp/body\""
}

func parseHTTPFetchOutput(data []byte, maxBody int) (WorkspaceHTTPFetchResult, error) {
	reader := bytes.NewReader(data)
	readLine := func() (string, error) {
		var line []byte
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return "", err
			}
			if b == '\n' {
				return string(line), nil
			}
			line = append(line, b)
		}
	}
	magic, err := readLine()
	if err != nil || magic != "CODER_HTTP_FETCH_V1" {
		return WorkspaceHTTPFetchResult{}, xerrors.New("invalid http_fetch helper response")
	}
	lengths := make([]int, 3)
	for i := range lengths {
		line, err := readLine()
		if err != nil {
			return WorkspaceHTTPFetchResult{}, xerrors.New("truncated http_fetch helper response")
		}
		length, err := strconv.Atoi(line)
		if err != nil || length < 0 {
			return WorkspaceHTTPFetchResult{}, xerrors.New("invalid http_fetch helper response length")
		}
		lengths[i] = length
	}
	remaining := reader.Len()
	if lengths[0]+lengths[1]+lengths[2] != remaining || lengths[2] > maxBody {
		return WorkspaceHTTPFetchResult{}, xerrors.New("invalid http_fetch helper response payload")
	}
	payload := make([]byte, remaining)
	if _, err := reader.Read(payload); err != nil {
		return WorkspaceHTTPFetchResult{}, err
	}
	meta := string(payload[:lengths[0]])
	rawHeaders := string(payload[lengths[0] : lengths[0]+lengths[1]])
	body := payload[lengths[0]+lengths[1]:]

	metaParts := strings.SplitN(meta, "\n", 2)
	if len(metaParts) != 2 {
		return WorkspaceHTTPFetchResult{}, xerrors.New("invalid http_fetch curl metadata")
	}
	status, err := strconv.Atoi(strings.TrimSpace(metaParts[0]))
	if err != nil {
		return WorkspaceHTTPFetchResult{}, xerrors.Errorf("parse HTTP status: %w", err)
	}
	result := WorkspaceHTTPFetchResult{
		Status:       status,
		EffectiveURL: strings.TrimSpace(metaParts[1]),
		Headers:      parseFinalHTTPHeaders(rawHeaders),
		BodyBytes:    len(body),
		Encoding:     "text",
	}
	if utf8.Valid(body) {
		result.Body = string(body)
	} else {
		result.Encoding = "base64"
		result.Body = base64.StdEncoding.EncodeToString(body)
	}
	return result, nil
}

func parseFinalHTTPHeaders(raw string) map[string][]string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	blocks := strings.Split(normalized, "\n\n")
	var final string
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if strings.HasPrefix(block, "HTTP/") {
			final = block
		}
	}
	if final == "" {
		return nil
	}
	headers := make(map[string][]string)
	lines := strings.Split(final, "\n")
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name != "" {
			headers[name] = append(headers[name], value)
		}
	}
	return headers
}
