package agentproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/spf13/afero"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/agent/agentchat"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/agent/agentgit"
	"github.com/coder/coder/v2/agent/usershell"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	// maxWaitDuration is the maximum time a blocking
	// process output request can wait, regardless of
	// what the client requests.
	maxWaitDuration = 5 * time.Minute
)

// API exposes process-related operations through the agent.
type API struct {
	logger    slog.Logger
	manager   *manager
	pathStore *agentgit.PathStore
}

// NewAPI creates a new process API handler.
func NewAPI(logger slog.Logger, execer agentexec.Execer, fs afero.Fs, pathStore *agentgit.PathStore, envInfo usershell.EnvInfoer, updateEnv func(current []string) (updated []string, err error), workingDir func() string) *API {
	return &API{
		logger:    logger,
		manager:   newManager(logger, execer, fs, envInfo, updateEnv, workingDir),
		pathStore: pathStore,
	}
}

// Close shuts down the process manager, killing all running
// processes.
func (api *API) Close() error {
	return api.manager.Close()
}

// Routes returns the HTTP handler for process-related routes.
func (api *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/start", api.handleStartProcess)
	r.Get("/list", api.handleListProcesses)
	r.Get("/{id}/output", api.handleProcessOutput)
	r.Post("/{id}/input", api.handleProcessInput)
	r.Post("/{id}/signal", api.handleSignalProcess)
	return r
}

// handleStartProcess starts a new process.
func (api *API) handleStartProcess(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req workspacesdk.StartProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Request body must be valid JSON.",
			Detail:  err.Error(),
		})
		return
	}

	if (req.Command == "") == (len(req.Argv) == 0) {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Exactly one of command or argv is required.",
		})
		return
	}
	if len(req.Argv) > 0 && req.Argv[0] == "" {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "argv[0] must not be empty.",
		})
		return
	}
	if len(req.Stdin) > workspacesdk.MaxProcessInputBytes {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: fmt.Sprintf("stdin cannot exceed %d bytes.", workspacesdk.MaxProcessInputBytes),
		})
		return
	}

	var chatID string
	if chatContext, ok := agentchat.FromContext(ctx); ok {
		chatID = chatContext.ID.String()
	}

	proc, err := api.manager.start(req, chatID)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to start process.",
			Detail:  err.Error(),
		})
		return
	}

	// Notify git watchers after the process finishes so that
	// file changes made by the command are visible in the scan.
	// If a workdir is provided, track it as a path as well.
	if api.pathStore != nil {
		if chatContext, ok := agentchat.FromContext(ctx); ok {
			allIDs := append([]uuid.UUID{chatContext.ID}, chatContext.AncestorIDs...)
			go func() {
				<-proc.done
				if req.WorkDir != "" {
					api.pathStore.AddPaths(allIDs, []string{req.WorkDir})
				} else {
					api.pathStore.Notify(allIDs)
				}
			}()
		}
	}

	httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.StartProcessResponse{
		ID:      proc.id,
		Started: true,
	})
}

// handleListProcesses lists all tracked processes.
func (api *API) handleListProcesses(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var chatID string
	if chatContext, ok := agentchat.FromContext(ctx); ok {
		chatID = chatContext.ID.String()
	}

	infos := api.manager.list(chatID)

	// Sort by running state (running first), then by started_at
	// descending so the most recent processes appear first.
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Running != infos[j].Running {
			return infos[i].Running
		}
		return infos[i].StartedAt > infos[j].StartedAt
	})

	httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.ListProcessesResponse{
		Processes: infos,
	})
}

// handleProcessOutput returns the output of a process.
func (api *API) handleProcessOutput(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := api.logger.With(agentchat.Fields(ctx)...)

	id := chi.URLParam(r, "id")
	proc, ok := api.manager.get(id)
	if !ok {
		httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
			Message: fmt.Sprintf("Process %q not found.", id),
		})
		return
	}

	// Enforce chat ID isolation. If the request carries
	// a chat context, only allow access to processes
	// belonging to that chat.
	if chatContext, ok := agentchat.FromContext(ctx); ok {
		if proc.chatID != "" && proc.chatID != chatContext.ID.String() {
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
				Message: fmt.Sprintf("Process %q not found.", id),
			})
			return
		}
	}

	// Check for blocking/incremental mode via query params.
	query := r.URL.Query()
	waitStr := query.Get("wait")
	wantWait := waitStr == "true"
	var cursor *int64
	if raw := query.Get("cursor"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "cursor must be a non-negative integer."})
			return
		}
		cursor = &parsed
	}
	limit := 0
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "limit must be a positive integer."})
			return
		}
		limit = parsed
	}

	// A cursor ahead of the currently written stream can happen after a stale
	// client checkpoint or process recovery. Treat it as the current end rather
	// than waiting for the process to produce bytes up to an impossible offset.
	if cursor != nil {
		current := int64(proc.buf.TotalWritten())
		if *cursor > current {
			clamped := current
			cursor = &clamped
		}
	}

	if wantWait {
		// Extend the write deadline so the HTTP server's
		// WriteTimeout does not kill the connection while
		// we block.
		rc := http.NewResponseController(rw)
		// Add headroom beyond the wait timeout so there's time to
		// write the response after the blocking wait completes.
		if err := rc.SetWriteDeadline(time.Now().Add(maxWaitDuration + 30*time.Second)); err != nil {
			logger.Error(ctx, "extend write deadline for blocking process output",
				slog.Error(err),
			)
		}

		// Cap the wait at maxWaitDuration regardless of
		// client-supplied timeout.
		waitCtx, waitCancel := context.WithTimeout(ctx, maxWaitDuration)
		defer waitCancel()

		if cursor != nil {
			_ = proc.waitForOutputSince(waitCtx, cursor)
		} else {
			_ = proc.waitForOutput(waitCtx)
		}
		// Fall through to read snapshot below.
	}

	// Read info before output to avoid a TOCTOU race. The exit
	// goroutine completes all buffer writes (cmd.Wait) before
	// setting running=false, so if info reports the process as
	// exited, the subsequent output read is guaranteed to reflect
	// the final buffer state.
	info := proc.info()
	if cursor != nil {
		output, nextCursor, gapBytes, hasMore := proc.buf.ReadSince(*cursor, limit)
		httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.ProcessOutputResponse{
			Output:     output,
			Running:    info.Running,
			ExitCode:   info.ExitCode,
			NextCursor: nextCursor,
			GapBytes:   gapBytes,
			HasMore:    hasMore,
		})
		return
	}
	output, truncated := proc.output()

	httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.ProcessOutputResponse{
		Output:    output,
		Truncated: truncated,
		Running:   info.Running,
		ExitCode:  info.ExitCode,
	})
}

func (api *API) handleProcessInput(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	if chatContext, ok := agentchat.FromContext(ctx); ok {
		proc, procOK := api.manager.get(id)
		if procOK && proc.chatID != "" && proc.chatID != chatContext.ID.String() {
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{Message: fmt.Sprintf("Process %q not found.", id)})
			return
		}
	}

	var req workspacesdk.ProcessInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "Request body must be valid JSON.", Detail: err.Error()})
		return
	}
	if req.Data == "" && !req.Close {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "data must be non-empty or close must be true."})
		return
	}
	if len(req.Data) > workspacesdk.MaxProcessInputBytes {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: fmt.Sprintf("data cannot exceed %d bytes.", workspacesdk.MaxProcessInputBytes),
		})
		return
	}
	if err := api.manager.input(id, req.Data, req.Close); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errProcessNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, errProcessNotRunning) {
			status = http.StatusConflict
		}
		httpapi.Write(ctx, rw, status, codersdk.Response{Message: err.Error()})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.Response{Message: "Process input sent."})
}

// handleSignalProcess sends a signal to a running process.
func (api *API) handleSignalProcess(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := chi.URLParam(r, "id")

	// Enforce chat ID isolation.
	if chatContext, ok := agentchat.FromContext(ctx); ok {
		proc, procOK := api.manager.get(id)
		if procOK && proc.chatID != "" && proc.chatID != chatContext.ID.String() {
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
				Message: fmt.Sprintf("Process %q not found.", id),
			})
			return
		}
	}

	var req workspacesdk.SignalProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Request body must be valid JSON.",
			Detail:  err.Error(),
		})
		return
	}

	if req.Signal == "" {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: "Signal is required.",
		})
		return
	}

	if req.Signal != "kill" && req.Signal != "terminate" {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message: fmt.Sprintf(
				"Unsupported signal %q. Use \"kill\" or \"terminate\".",
				req.Signal,
			),
		})
		return
	}

	if err := api.manager.signal(id, req.Signal); err != nil {
		switch {
		case errors.Is(err, errProcessNotFound):
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
				Message: fmt.Sprintf("Process %q not found.", id),
			})
		case errors.Is(err, errProcessNotRunning):
			httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
				Message: fmt.Sprintf(
					"Process %q is not running.", id,
				),
			})
		default:
			httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
				Message: "Failed to signal process.",
				Detail:  err.Error(),
			})
		}
		return
	}

	httpapi.Write(ctx, rw, http.StatusOK, codersdk.Response{
		Message: fmt.Sprintf(
			"Signal %q sent to process %q.", req.Signal, id,
		),
	})
}
