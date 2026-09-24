package workspacesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/xerrors"
)

const maxSemanticAgentResponseBytes = 64 << 20

// SemanticPosition is a public 1-based source position. Column counts Unicode
// code points rather than bytes or UTF-16 code units.
type SemanticPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// SemanticRange is a half-open source range: Start is inclusive and End is
// exclusive.
type SemanticRange struct {
	Start SemanticPosition `json:"start"`
	End   SemanticPosition `json:"end"`
}

// SemanticTarget is a reusable semantic locator. find_symbol returns this
// shape, and find_references/find_implementations accept it unchanged.
type SemanticTarget struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// SemanticContext is a bounded literal source excerpt surrounding a semantic
// location.
type SemanticContext struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Text      string `json:"text"`
}

// SemanticCoverage describes how confidently the backend covered the requested
// semantic scope.
type SemanticCoverage struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// SemanticSymbol is the normalized public representation of a semantic code
// symbol.
type SemanticSymbol struct {
	Name           string           `json:"name"`
	Kind           string           `json:"kind"`
	Language       string           `json:"language"`
	Path           string           `json:"path"`
	Container      string           `json:"container,omitempty"`
	Detail         string           `json:"detail,omitempty"`
	Range          *SemanticRange   `json:"range,omitempty"`
	SelectionRange SemanticRange    `json:"selection_range"`
	Locator        SemanticTarget   `json:"locator"`
	Context        *SemanticContext `json:"context,omitempty"`
}

type SemanticFindSymbolsRequest struct {
	Root         string   `json:"root"`
	Query        string   `json:"query"`
	Match        string   `json:"match,omitempty"`
	Kinds        []string `json:"kinds,omitempty"`
	Limit        int      `json:"limit"`
	ContextLines int      `json:"context_lines,omitempty"`
}

type SemanticFindSymbolsResponse struct {
	Symbols       []SemanticSymbol `json:"symbols"`
	ReturnedCount int              `json:"returned_count"`
	ObservedCount int              `json:"observed_count"`
	Truncated     bool             `json:"truncated"`
	Coverage      SemanticCoverage `json:"coverage"`
}

type SemanticContainingSymbol struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type SemanticReference struct {
	Path             string                    `json:"path"`
	Language         string                    `json:"language"`
	Range            SemanticRange             `json:"range"`
	Locator          SemanticTarget            `json:"locator"`
	ContainingSymbol *SemanticContainingSymbol `json:"containing_symbol,omitempty"`
	Context          *SemanticContext          `json:"context,omitempty"`
}

type SemanticFindReferencesRequest struct {
	Target             SemanticTarget `json:"target"`
	IncludeDeclaration bool           `json:"include_declaration,omitempty"`
	ScopePath          string         `json:"scope_path,omitempty"`
	Limit              int            `json:"limit"`
	ContextLines       int            `json:"context_lines,omitempty"`
}

type SemanticFindReferencesResponse struct {
	Target        SemanticTarget      `json:"target"`
	References    []SemanticReference `json:"references"`
	ReturnedCount int                 `json:"returned_count"`
	ObservedCount int                 `json:"observed_count"`
	Truncated     bool                `json:"truncated"`
	Coverage      SemanticCoverage    `json:"coverage"`
}

type SemanticImplementation struct {
	Path           string           `json:"path"`
	Language       string           `json:"language"`
	Range          SemanticRange    `json:"range"`
	SelectionRange *SemanticRange   `json:"selection_range,omitempty"`
	Locator        SemanticTarget   `json:"locator"`
	Name           string           `json:"name,omitempty"`
	Kind           string           `json:"kind,omitempty"`
	Container      string           `json:"container,omitempty"`
	Detail         string           `json:"detail,omitempty"`
	Context        *SemanticContext `json:"context,omitempty"`
}

type SemanticFindImplementationsRequest struct {
	Target       SemanticTarget `json:"target"`
	ScopePath    string         `json:"scope_path,omitempty"`
	Limit        int            `json:"limit"`
	ContextLines int            `json:"context_lines,omitempty"`
}

type SemanticFindImplementationsResponse struct {
	Target          SemanticTarget           `json:"target"`
	Implementations []SemanticImplementation `json:"implementations"`
	ReturnedCount   int                      `json:"returned_count"`
	ObservedCount   int                      `json:"observed_count"`
	Truncated       bool                     `json:"truncated"`
	Coverage        SemanticCoverage         `json:"coverage"`
}

type SemanticRelatedInformation struct {
	Path    string        `json:"path"`
	Range   SemanticRange `json:"range"`
	Message string        `json:"message"`
}

type SemanticDiagnostic struct {
	Path               string                       `json:"path"`
	Severity           string                       `json:"severity"`
	Message            string                       `json:"message"`
	Range              SemanticRange                `json:"range"`
	Source             string                       `json:"source,omitempty"`
	Code               string                       `json:"code,omitempty"`
	CodeHref           string                       `json:"code_href,omitempty"`
	Tags               []string                     `json:"tags,omitempty"`
	RelatedInformation []SemanticRelatedInformation `json:"related_information,omitempty"`
	Context            *SemanticContext             `json:"context,omitempty"`
}

type SemanticDiagnosticFileStatus struct {
	Path            string `json:"path"`
	Language        string `json:"language,omitempty"`
	Status          string `json:"status"`
	Freshness       string `json:"freshness,omitempty"`
	DiagnosticCount int    `json:"diagnostic_count"`
	Message         string `json:"message,omitempty"`
}

type SemanticDiagnosticsRequest struct {
	Paths                     []string `json:"paths"`
	MinimumSeverity           string   `json:"minimum_severity,omitempty"`
	IncludeRelatedInformation bool     `json:"include_related_information,omitempty"`
	Limit                     int      `json:"limit"`
	ContextLines              int      `json:"context_lines,omitempty"`
}

type SemanticDiagnosticsResponse struct {
	Files         []SemanticDiagnosticFileStatus `json:"files"`
	Diagnostics   []SemanticDiagnostic           `json:"diagnostics"`
	ReturnedCount int                            `json:"returned_count"`
	ObservedCount int                            `json:"observed_count"`
	Truncated     bool                           `json:"truncated"`
	Coverage      SemanticCoverage               `json:"coverage"`
}

// SemanticErrorResponse is the typed wire error returned by workspace semantic
// endpoints. It keeps a stable machine-readable code while preserving an
// actionable human message.
type SemanticErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// SemanticError is returned by AgentConn semantic methods for typed semantic
// failures.
type SemanticError struct {
	Code    string
	Message string
	Detail  string
}

func (e *SemanticError) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Code != "" && e.Detail != "":
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Detail)
	case e.Code != "":
		return fmt.Sprintf("[%s] %s", e.Code, e.Message)
	case e.Detail != "":
		return e.Message + ": " + e.Detail
	default:
		return e.Message
	}
}

func (c *agentConn) semanticRequest(ctx context.Context, path string, req any, dst any) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return xerrors.Errorf("marshal semantic request: %w", err)
	}
	res, err := c.apiRequest(ctx, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return xerrors.Errorf("do semantic request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		if readErr != nil {
			return xerrors.Errorf("read semantic error response: %w", readErr)
		}
		return semanticResponseError(res.StatusCode, body)
	}
	limited := io.LimitReader(res.Body, maxSemanticAgentResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return xerrors.Errorf("read semantic response: %w", err)
	}
	if len(body) > maxSemanticAgentResponseBytes {
		return &SemanticError{
			Code:    "response_too_large",
			Message: fmt.Sprintf("Semantic response exceeds the internal Agent safety limit of %d bytes; retry with a positive limit and/or lower context_lines.", maxSemanticAgentResponseBytes),
		}
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return xerrors.Errorf("decode semantic response: %w", err)
	}
	return nil
}

func semanticResponseError(statusCode int, body []byte) error {
	var semanticErr SemanticErrorResponse
	if json.Unmarshal(body, &semanticErr) == nil && semanticErr.Code != "" {
		return &SemanticError{Code: semanticErr.Code, Message: semanticErr.Message, Detail: semanticErr.Detail}
	}

	if statusCode == http.StatusNotFound && strings.TrimSpace(strings.ToLower(string(body))) == "404 page not found" {
		return &SemanticError{
			Code:    "semantic_backend_unavailable",
			Message: "Workspace Agent is outdated and does not support semantic code tools; restart the workspace to update the Agent, then retry.",
		}
	}
	return xerrors.Errorf("semantic endpoint returned HTTP %d: %s", statusCode, string(body))
}

func (c *agentConn) FindSemanticSymbols(ctx context.Context, req SemanticFindSymbolsRequest) (SemanticFindSymbolsResponse, error) {
	var resp SemanticFindSymbolsResponse
	err := c.semanticRequest(ctx, "/api/v0/semantic/symbols", req, &resp)
	return resp, err
}

func (c *agentConn) FindSemanticReferences(ctx context.Context, req SemanticFindReferencesRequest) (SemanticFindReferencesResponse, error) {
	var resp SemanticFindReferencesResponse
	err := c.semanticRequest(ctx, "/api/v0/semantic/references", req, &resp)
	return resp, err
}

func (c *agentConn) FindSemanticImplementations(ctx context.Context, req SemanticFindImplementationsRequest) (SemanticFindImplementationsResponse, error) {
	var resp SemanticFindImplementationsResponse
	err := c.semanticRequest(ctx, "/api/v0/semantic/implementations", req, &resp)
	return resp, err
}

func (c *agentConn) GetSemanticDiagnostics(ctx context.Context, req SemanticDiagnosticsRequest) (SemanticDiagnosticsResponse, error) {
	var resp SemanticDiagnosticsResponse
	err := c.semanticRequest(ctx, "/api/v0/semantic/diagnostics", req, &resp)
	return resp, err
}
