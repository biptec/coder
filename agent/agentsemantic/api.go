package agentsemantic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// API exposes semantic code operations through the workspace Agent.
type API struct {
	manager *Manager
}

func NewAPI(manager *Manager) *API {
	return &API{manager: manager}
}

func (a *API) Close() error {
	if a == nil || a.manager == nil {
		return nil
	}
	return a.manager.Close()
}

func (a *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/symbols", a.handleFindSymbols)
	r.Post("/references", a.handleFindReferences)
	r.Post("/implementations", a.handleFindImplementations)
	r.Post("/diagnostics", a.handleDiagnostics)
	return r
}

func decodeStrictJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return xerrors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func (a *API) handleFindSymbols(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	started := time.Now()
	var req workspacesdk.SemanticFindSymbolsRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeSemanticError(ctx, rw, semanticError(CodeInvalidPath, "Request body must be valid semantic find-symbol JSON.", err))
		return
	}
	resp, err := a.manager.FindSymbols(ctx, req)
	if err != nil {
		a.logRequest(ctx, "find_symbol", started, 0, "", err)
		writeSemanticError(ctx, rw, err)
		return
	}
	a.logRequest(ctx, "find_symbol", started, resp.ReturnedCount, resp.Coverage.Status, nil)
	httpapi.Write(ctx, rw, http.StatusOK, resp)
}

func (a *API) handleFindReferences(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	started := time.Now()
	var req workspacesdk.SemanticFindReferencesRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeSemanticError(ctx, rw, semanticError(CodeInvalidPath, "Request body must be valid semantic reference JSON.", err))
		return
	}
	resp, err := a.manager.FindReferences(ctx, req)
	if err != nil {
		a.logRequest(ctx, "find_references", started, 0, "", err)
		writeSemanticError(ctx, rw, err)
		return
	}
	a.logRequest(ctx, "find_references", started, resp.ReturnedCount, resp.Coverage.Status, nil)
	httpapi.Write(ctx, rw, http.StatusOK, resp)
}

func (a *API) handleFindImplementations(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	started := time.Now()
	var req workspacesdk.SemanticFindImplementationsRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeSemanticError(ctx, rw, semanticError(CodeInvalidPath, "Request body must be valid semantic implementation JSON.", err))
		return
	}
	resp, err := a.manager.FindImplementations(ctx, req)
	if err != nil {
		a.logRequest(ctx, "find_implementations", started, 0, "", err)
		writeSemanticError(ctx, rw, err)
		return
	}
	a.logRequest(ctx, "find_implementations", started, resp.ReturnedCount, resp.Coverage.Status, nil)
	httpapi.Write(ctx, rw, http.StatusOK, resp)
}

func (a *API) handleDiagnostics(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	started := time.Now()
	var req workspacesdk.SemanticDiagnosticsRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeSemanticError(ctx, rw, semanticError(CodeInvalidPath, "Request body must be valid semantic diagnostics JSON.", err))
		return
	}
	resp, err := a.manager.GetDiagnostics(ctx, req)
	if err != nil {
		a.logRequest(ctx, "get_diagnostics", started, 0, "", err)
		writeSemanticError(ctx, rw, err)
		return
	}
	a.logRequest(ctx, "get_diagnostics", started, resp.ReturnedCount, resp.Coverage.Status, nil)
	httpapi.Write(ctx, rw, http.StatusOK, resp)
}

func (a *API) logRequest(ctx context.Context, tool string, started time.Time, resultCount int, coverage string, err error) {
	if a == nil || a.manager == nil {
		return
	}
	fields := []slog.Field{
		slog.F("tool", tool),
		slog.F("duration_ms", time.Since(started).Milliseconds()),
	}
	if err != nil {
		fields = append(fields, slog.F("error_code", semanticErrorCode(err)))
	} else {
		fields = append(fields,
			slog.F("result_count", resultCount),
			slog.F("coverage", coverage),
		)
	}
	a.manager.logger.Debug(ctx, "semantic request completed", fields...)
}

func writeSemanticError(ctx context.Context, rw http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := CodeRequestFailed
	message := "Semantic request failed."
	detail := ""

	var semanticErr *Error
	if errors.As(err, &semanticErr) {
		code = semanticErr.Code
		message = semanticErr.Message
		detail = semanticErr.Detail
		switch semanticErr.Code {
		case CodeInvalidPath, CodeInvalidPosition, CodeUnsupportedLanguage, CodeCapabilityUnsupported:
			status = http.StatusBadRequest
		case CodeBackendUnavailable, CodeBackendStartFailed, CodeBackendNotReady:
			status = http.StatusServiceUnavailable
		case CodeRequestFailed:
			status = http.StatusBadGateway
		case CodeResponseTooLarge:
			status = http.StatusRequestEntityTooLarge
		}
	} else if err != nil {
		detail = err.Error()
	}

	httpapi.Write(ctx, rw, status, workspacesdk.SemanticErrorResponse{
		Code: code, Message: message, Detail: detail,
	})
}
