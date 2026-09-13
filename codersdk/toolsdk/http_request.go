package toolsdk

import (
	"context"
	"encoding/base64"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const maxHTTPRequestBodyBytes = 4 << 20

type WorkspaceHTTPRequestArgs struct {
	Workspace       string            `json:"workspace"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	BodyEncoding    string            `json:"body_encoding,omitempty"`
	Host            string            `json:"host,omitempty"`
	FollowRedirects *bool             `json:"follow_redirects,omitempty"`
	TimeoutMs       *int              `json:"timeout_ms,omitempty"`
	MaxBodyBytes    int               `json:"max_body_bytes,omitempty"`
}

var WorkspaceHTTPRequest = Tool[WorkspaceHTTPRequestArgs, WorkspaceHTTPFetchResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceHTTPRequest,
		Description: `Send a potentially mutating HTTP or HTTPS request from the workspace or an optional configured SSH alias.

Supported methods are POST, PUT, PATCH, and DELETE. Use http_fetch for GET or HEAD. Request headers and body values are redacted from stored MCP activity metadata. Response bodies are bounded; binary responses are returned as base64.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"url":       map[string]any{"type": "string", "description": "Absolute HTTP or HTTPS URL."},
				"method": map[string]any{
					"type":        "string",
					"description": "Mutating HTTP method.",
					"enum":        []string{"POST", "PUT", "PATCH", "DELETE"},
				},
				"headers": map[string]any{
					"type":                 "object",
					"description":          "Optional request headers. Values are redacted from stored MCP activity metadata.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"body":          map[string]any{"type": "string", "description": "Optional request body. Maximum decoded size is 4 MiB. Stored MCP activity metadata redacts the value."},
				"body_encoding": map[string]any{"type": "string", "description": "Encoding of body. Defaults to text.", "enum": []string{"text", "base64"}},
				"host":          map[string]any{"type": "string", "description": "Optional SSH alias returned by remote_hosts."},
				"follow_redirects": map[string]any{
					"type":        "boolean",
					"description": "Follow HTTP redirects. Defaults to false for mutating requests; enable explicitly when intended.",
					"default":     false,
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
			Required: []string{"workspace", "url", "method"},
		},
	},
	MCPAnnotations:     mcpDestructiveOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceHTTPRequestArgs) (WorkspaceHTTPFetchResult, error) {
		if err := validateRemoteTarget(args.Host, ""); err != nil {
			return WorkspaceHTTPFetchResult{}, err
		}
		parsedURL, err := url.Parse(args.URL)
		if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return WorkspaceHTTPFetchResult{}, xerrors.New("url must be an absolute http or https URL")
		}
		method := strings.ToUpper(strings.TrimSpace(args.Method))
		switch method {
		case "POST", "PUT", "PATCH", "DELETE":
		default:
			return WorkspaceHTTPFetchResult{}, xerrors.New("method must be POST, PUT, PATCH, or DELETE")
		}
		for name, value := range args.Headers {
			if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n:") {
				return WorkspaceHTTPFetchResult{}, xerrors.Errorf("invalid HTTP header name %q", name)
			}
			if strings.ContainsAny(value, "\r\n") {
				return WorkspaceHTTPFetchResult{}, xerrors.Errorf("HTTP header %q contains a newline", name)
			}
		}

		bodyEncoding := strings.ToLower(strings.TrimSpace(args.BodyEncoding))
		if bodyEncoding == "" {
			bodyEncoding = "text"
		}
		var body []byte
		switch bodyEncoding {
		case "text":
			body = []byte(args.Body)
		case "base64":
			decoded, err := base64.StdEncoding.DecodeString(args.Body)
			if err != nil {
				return WorkspaceHTTPFetchResult{}, xerrors.Errorf("decode body: %w", err)
			}
			body = decoded
		default:
			return WorkspaceHTTPFetchResult{}, xerrors.New("body_encoding must be text or base64")
		}
		if len(body) > maxHTTPRequestBodyBytes {
			return WorkspaceHTTPFetchResult{}, xerrors.Errorf("request body cannot exceed %d bytes", maxHTTPRequestBodyBytes)
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

		followRedirects := false
		if args.FollowRedirects != nil {
			followRedirects = *args.FollowRedirects
		}
		command := buildHTTPRequestCommand(args.URL, method, args.Headers, maxBody, followRedirects, method != "DELETE" || len(body) > 0)
		output, err := runHelperCommand(operationCtx, conn, workspacesdk.RunCommandRequest{
			Host:        args.Host,
			Command:     command,
			StdinBase64: base64.StdEncoding.EncodeToString(body),
		})
		if err != nil {
			return WorkspaceHTTPFetchResult{}, xerrors.Errorf("http request: %w", err)
		}
		return parseHTTPFetchOutput(output, maxBody)
	},
}

//nolint:revive // followRedirects and sendBody are direct HTTP semantics, not hidden control coupling.
func buildHTTPRequestCommand(rawURL, method string, headers map[string]string, maxBody int, followRedirects, sendBody bool) string {
	curlArgs := []string{"curl", "--silent", "--show-error", "--proto", "=http,https", "--proto-redir", "=http,https"}
	if followRedirects {
		curlArgs = append(curlArgs, "--location")
	}
	curlArgs = append(curlArgs, "--request", method)
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		curlArgs = append(curlArgs, "--header", key+": "+headers[key])
	}
	if sendBody {
		curlArgs = append(curlArgs, "--data-binary", "@-")
	}
	curlArgs = append(curlArgs,
		"--dump-header", `"$tmp/headers"`,
		"--max-filesize", strconv.Itoa(maxBody),
		"--output", `"$tmp/body"`,
		"--write-out", "%{http_code}\\n%{url_effective}",
		"--url", rawURL,
	)

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
