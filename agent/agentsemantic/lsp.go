package agentsemantic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/xerrors"
)

const (
	maxBackendStderrBytes = 64 << 10
	maxLSPMessageBytes    = 64 << 20
)

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetRange          lspRange `json:"targetRange"`
	TargetSelectionRange lspRange `json:"targetSelectionRange"`
}

type lspDiagnostic struct {
	Range           lspRange `json:"range"`
	Severity        int      `json:"severity,omitempty"`
	Code            any      `json:"code,omitempty"`
	CodeDescription *struct {
		Href string `json:"href"`
	} `json:"codeDescription,omitempty"`
	Source             string `json:"source,omitempty"`
	Message            string `json:"message"`
	Tags               []int  `json:"tags,omitempty"`
	RelatedInformation []struct {
		Location lspLocation `json:"location"`
		Message  string      `json:"message"`
	} `json:"relatedInformation,omitempty"`
}

type lspPublishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Version     *int            `json:"version,omitempty"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspDiagnosticSnapshot struct {
	seq         uint64
	version     *int
	diagnostics []lspDiagnostic
}

type lspInitializeResult struct {
	Capabilities struct {
		PositionEncoding        string          `json:"positionEncoding,omitempty"`
		ReferencesProvider      json.RawMessage `json:"referencesProvider,omitempty"`
		ImplementationProvider  json.RawMessage `json:"implementationProvider,omitempty"`
		DocumentSymbolProvider  json.RawMessage `json:"documentSymbolProvider,omitempty"`
		WorkspaceSymbolProvider json.RawMessage `json:"workspaceSymbolProvider,omitempty"`
		DiagnosticProvider      json.RawMessage `json:"diagnosticProvider,omitempty"`
	} `json:"capabilities"`
}

type lspCapabilities struct {
	positionEncoding string
	references       bool
	implementations  bool
	documentSymbols  bool
	workspaceSymbols bool
	pullDiagnostics  bool
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
	err    error
}

type boundedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		return len(p), nil
	}
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type lspClient struct {
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *boundedBuffer

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]chan rpcResponse

	diagMu      sync.Mutex
	diagSeq     uint64
	diagnostics map[string]lspDiagnosticSnapshot
	diagNotify  chan struct{}

	rootURI  string
	rootName string

	done      chan struct{}
	closeOnce sync.Once
	errMu     sync.Mutex
	readErr   error

	capabilities lspCapabilities
}

func newLSPClient(parent context.Context, root string, command func(context.Context) *exec.Cmd) (*lspClient, error) {
	ctx, cancel := context.WithCancel(parent)
	cmd := command(ctx)
	if cmd == nil {
		cancel()
		return nil, xerrors.New("language-server command factory returned nil command")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, xerrors.Errorf("create language-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, xerrors.Errorf("create language-server stdout: %w", err)
	}
	stderr := &boundedBuffer{max: maxBackendStderrBytes}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, xerrors.Errorf("start language server: %w", err)
	}

	client := &lspClient{
		ctx:         ctx,
		cancel:      cancel,
		cmd:         cmd,
		stdin:       stdin,
		stdout:      bufio.NewReaderSize(stdout, 64<<10),
		stderr:      stderr,
		pending:     make(map[string]chan rpcResponse),
		diagnostics: make(map[string]lspDiagnosticSnapshot),
		diagNotify:  make(chan struct{}, 1),
		rootURI:     pathToFileURI(root),
		rootName:    filepath.Base(root),
		done:        make(chan struct{}),
	}
	go client.readLoop()
	go func() {
		err := cmd.Wait()
		if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			client.fail(xerrors.Errorf("language server exited: %w", err))
		} else {
			client.fail(io.EOF)
		}
	}()
	return client, nil
}

func (c *lspClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"processId": nil,
		"clientInfo": map[string]any{
			"name":    "coder-workspace-semantic",
			"version": "1",
		},
		"rootUri": c.rootURI,
		"workspaceFolders": []map[string]any{{
			"uri":  c.rootURI,
			"name": c.rootName,
		}},
		"capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-8", "utf-16"},
			},
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    true,
				"symbol": map[string]any{
					"symbolKind": map[string]any{
						"valueSet": integerRange(1, 26),
					},
				},
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"dynamicRegistration": false,
					"willSave":            false,
					"didSave":             false,
				},
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
					"symbolKind": map[string]any{
						"valueSet": integerRange(1, 26),
					},
				},
				"publishDiagnostics": map[string]any{
					"relatedInformation":     true,
					"versionSupport":         true,
					"codeDescriptionSupport": true,
					"dataSupport":            false,
					"tagSupport": map[string]any{
						"valueSet": []int{1, 2},
					},
				},
				"diagnostic": map[string]any{
					"dynamicRegistration":    false,
					"relatedDocumentSupport": true,
				},
			},
		},
	}
	var result lspInitializeResult
	if err := c.requestInto(ctx, "initialize", params, &result); err != nil {
		return err
	}
	encoding := strings.ToLower(strings.TrimSpace(result.Capabilities.PositionEncoding))
	if encoding == "" {
		encoding = "utf-16"
	}
	switch encoding {
	case "utf-8", "utf-16", "utf-32":
	default:
		return xerrors.Errorf("unsupported LSP position encoding %q", encoding)
	}
	c.capabilities = lspCapabilities{
		positionEncoding: encoding,
		references:       capabilityEnabled(result.Capabilities.ReferencesProvider),
		implementations:  capabilityEnabled(result.Capabilities.ImplementationProvider),
		documentSymbols:  capabilityEnabled(result.Capabilities.DocumentSymbolProvider),
		workspaceSymbols: capabilityEnabled(result.Capabilities.WorkspaceSymbolProvider),
		pullDiagnostics:  capabilityEnabled(result.Capabilities.DiagnosticProvider),
	}
	return c.notify(ctx, "initialized", map[string]any{})
}

func integerRange(first, last int) []int {
	out := make([]int, 0, last-first+1)
	for i := first; i <= last; i++ {
		out = append(out, i)
	}
	return out
}

func capabilityEnabled(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "false" {
		return false
	}
	return true
}

func (c *lspClient) requestInto(ctx context.Context, method string, params any, dst any) error {
	raw, err := c.request(ctx, method, params)
	if err != nil {
		return err
	}
	if dst == nil || string(raw) == "null" || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return xerrors.Errorf("decode %s response: %w", method, err)
	}
	return nil
}

func (c *lspClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	idRaw := strconv.FormatInt(id, 10)
	ch := make(chan rpcResponse, 1)

	c.pendingMu.Lock()
	c.pending[idRaw] = ch
	c.pendingMu.Unlock()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeMessage(msg); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, idRaw)
		c.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, idRaw)
		c.pendingMu.Unlock()
		_ = c.notify(context.Background(), "$/cancelRequest", map[string]any{"id": id})
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.lastError()
	case response := <-ch:
		if response.err != nil {
			return nil, response.err
		}
		if response.Error != nil {
			return nil, xerrors.Errorf("LSP %s failed (%d): %s", method, response.Error.Code, response.Error.Message)
		}
		return response.Result, nil
	}
}

func (c *lspClient) notify(ctx context.Context, method string, params any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *lspClient) writeMessage(message any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return xerrors.Errorf("marshal LSP message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return c.lastError()
	default:
	}
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return xerrors.Errorf("write LSP header: %w", err)
	}
	if _, err := c.stdin.Write(body); err != nil {
		return xerrors.Errorf("write LSP body: %w", err)
	}
	return nil
}

func (c *lspClient) readLoop() {
	for {
		body, err := readLSPMessage(c.stdout)
		if err != nil {
			c.fail(err)
			return
		}
		var envelope struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method,omitempty"`
			Params json.RawMessage `json:"params,omitempty"`
			Result json.RawMessage `json:"result,omitempty"`
			Error  *rpcError       `json:"error,omitempty"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			c.fail(xerrors.Errorf("decode LSP message: %w", err))
			return
		}
		if envelope.Method != "" {
			if len(envelope.ID) == 0 || string(envelope.ID) == "null" {
				c.handleNotification(envelope.Method, envelope.Params)
				continue
			}
			c.handleServerRequest(envelope.ID, envelope.Method, envelope.Params)
			continue
		}
		if len(envelope.ID) == 0 {
			continue
		}
		key := string(envelope.ID)
		c.pendingMu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.pendingMu.Unlock()
		if ch != nil {
			ch <- rpcResponse{Result: envelope.Result, Error: envelope.Error}
		}
	}
}

func readLSPMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, xerrors.Errorf("invalid LSP Content-Length %q", value)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, xerrors.New("LSP message missing Content-Length")
	}
	if contentLength > maxLSPMessageBytes {
		return nil, xerrors.Errorf("LSP message Content-Length %d exceeds internal safety limit %d", contentLength, maxLSPMessageBytes)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *lspClient) handleNotification(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var published lspPublishDiagnosticsParams
	if json.Unmarshal(params, &published) != nil || published.URI == "" {
		return
	}
	c.diagMu.Lock()
	c.diagSeq++
	snapshot := lspDiagnosticSnapshot{
		seq:         c.diagSeq,
		version:     published.Version,
		diagnostics: append([]lspDiagnostic(nil), published.Diagnostics...),
	}
	c.diagnostics[published.URI] = snapshot
	c.diagMu.Unlock()
	select {
	case c.diagNotify <- struct{}{}:
	default:
	}
}

func (c *lspClient) handleServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	var result any
	var rpcErr *rpcError
	switch method {
	case "workspace/configuration":
		var request struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(params, &request) == nil {
			values := make([]any, len(request.Items))
			for i := range values {
				values[i] = map[string]any{}
			}
			result = values
		} else {
			result = []any{}
		}
	case "workspace/workspaceFolders":
		result = []map[string]any{{"uri": c.rootURI, "name": c.rootName}}
	case "client/registerCapability",
		"client/unregisterCapability",
		"window/workDoneProgress/create",
		"workspace/semanticTokens/refresh",
		"workspace/diagnostic/refresh",
		"workspace/inlayHint/refresh",
		"workspace/codeLens/refresh":
		result = nil
	case "workspace/applyEdit":
		result = map[string]any{
			"applied":       false,
			"failureReason": "Coder semantic tools are read-only",
		}
	case "window/showMessageRequest":
		result = nil
	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not supported by Coder semantic client"}
	}

	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	_ = c.writeMessage(response)
}

func (c *lspClient) diagnosticSequence() uint64 {
	c.diagMu.Lock()
	defer c.diagMu.Unlock()
	return c.diagSeq
}

func (c *lspClient) latestDiagnostics(uri string) (lspDiagnosticSnapshot, bool) {
	c.diagMu.Lock()
	defer c.diagMu.Unlock()
	snapshot, ok := c.diagnostics[uri]
	return snapshot, ok
}

func (c *lspClient) waitForDiagnostics(ctx context.Context, uri string, after uint64, version int) (lspDiagnosticSnapshot, string, error) {
	for {
		if snapshot, ok := c.latestDiagnostics(uri); ok && snapshot.seq > after {
			if snapshot.version == nil {
				return snapshot, "unknown", nil
			}
			if *snapshot.version == version {
				return snapshot, "fresh", nil
			}
		}
		select {
		case <-ctx.Done():
			return lspDiagnosticSnapshot{}, "", ctx.Err()
		case <-c.done:
			return lspDiagnosticSnapshot{}, "", c.lastError()
		case <-c.diagNotify:
		}
	}
}

func (c *lspClient) fail(err error) {
	c.closeOnce.Do(func() {
		c.errMu.Lock()
		c.readErr = err
		c.errMu.Unlock()
		close(c.done)

		c.pendingMu.Lock()
		pending := c.pending
		c.pending = make(map[string]chan rpcResponse)
		c.pendingMu.Unlock()
		for _, ch := range pending {
			select {
			case ch <- rpcResponse{err: err}:
			default:
			}
		}
	})
}

func (c *lspClient) lastError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.readErr != nil {
		return c.readErr
	}
	return io.EOF
}

func (c *lspClient) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *lspClient) close(ctx context.Context) error {
	if c.alive() {
		_ = c.requestInto(ctx, "shutdown", nil, nil)
		_ = c.notify(context.Background(), "exit", nil)
	}
	c.cancel()
	select {
	case <-c.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (c *lspClient) stderrString() string {
	return strings.TrimSpace(c.stderr.String())
}

func pathToFileURI(path string) string {
	path = filepath.Clean(path)
	slash := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(slash) >= 2 && slash[1] == ':' {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}

func fileURIToPath(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", xerrors.Errorf("parse file URI: %w", err)
	}
	if parsed.Scheme != "file" {
		return "", xerrors.Errorf("unsupported semantic URI scheme %q", parsed.Scheme)
	}
	path := parsed.Path
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.Clean(filepath.FromSlash(path)), nil
}

func publicColumnToLSP(line string, publicColumn int, encoding string) (int, error) {
	if publicColumn < 1 {
		return 0, xerrors.New("column must be positive")
	}
	targetRunes := publicColumn - 1
	runeCount := utf8.RuneCountInString(line)
	if targetRunes > runeCount {
		return 0, xerrors.Errorf("column %d is beyond line length %d", publicColumn, runeCount+1)
	}
	prefixRunes := []rune(line)
	prefix := string(prefixRunes[:targetRunes])
	switch encoding {
	case "utf-8":
		return len(prefix), nil
	case "utf-16":
		return len(utf16.Encode([]rune(prefix))), nil
	case "utf-32":
		return targetRunes, nil
	default:
		return 0, xerrors.Errorf("unsupported position encoding %q", encoding)
	}
}

func lspColumnToPublic(line string, lspColumn int, encoding string) (int, error) {
	if lspColumn < 0 {
		return 0, xerrors.New("LSP column cannot be negative")
	}
	units := 0
	runes := 0
	for _, r := range line {
		if units == lspColumn {
			return runes + 1, nil
		}
		width := 1
		switch encoding {
		case "utf-8":
			width = utf8.RuneLen(r)
		case "utf-16":
			if r > 0xffff {
				width = 2
			}
		case "utf-32":
			width = 1
		default:
			return 0, xerrors.Errorf("unsupported position encoding %q", encoding)
		}
		if units+width > lspColumn {
			return 0, xerrors.Errorf("LSP column %d falls inside encoded character boundary", lspColumn)
		}
		units += width
		runes++
	}
	if units == lspColumn {
		return runes + 1, nil
	}
	return 0, xerrors.Errorf("LSP column %d is beyond line length", lspColumn)
}
