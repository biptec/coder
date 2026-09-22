package agentfiles

import (
	"container/heap"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/afero"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/agent/agentchat"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func filesystemStatus(err error) int {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound
	case errors.Is(err, os.ErrPermission):
		return http.StatusForbidden
	case errors.Is(err, os.ErrExist):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

type directoryQueueItem struct {
	info    workspacesdk.WorkspaceFileInfo
	level   int
	sortKey string
}

type directoryQueue []directoryQueueItem

func (q directoryQueue) Len() int           { return len(q) }
func (q directoryQueue) Less(i, j int) bool { return q[i].sortKey < q[j].sortKey }
func (q directoryQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *directoryQueue) Push(value any) {
	item, ok := value.(directoryQueueItem)
	if !ok {
		panic("directoryQueue: unexpected item type")
	}
	*q = append(*q, item)
}

func (q *directoryQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

func directoryRelativeKey(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func (api *API) fileInfo(path string) (workspacesdk.WorkspaceFileInfo, error) {
	var (
		info os.FileInfo
		err  error
	)
	if lstater, ok := api.filesystem.(afero.Lstater); ok {
		info, _, err = lstater.LstatIfPossible(path)
	} else {
		info, err = api.filesystem.Stat(path)
	}
	if err != nil {
		return workspacesdk.WorkspaceFileInfo{}, err
	}
	return workspacesdk.WorkspaceFileInfo{
		Path:        path,
		Name:        info.Name(),
		IsDir:       info.IsDir(),
		IsSymlink:   info.Mode()&os.ModeSymlink != 0,
		Size:        info.Size(),
		Mode:        info.Mode().String(),
		ModTimeUnix: info.ModTime().Unix(),
	}, nil
}

func (api *API) HandleListDirectoryV2(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req workspacesdk.ListDirectoryRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if !filepath.IsAbs(req.Path) {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "path must be absolute"})
		return
	}
	if req.Depth == 0 {
		req.Depth = 1
	}
	if req.Depth < 1 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "depth must be positive"})
		return
	}
	if req.Cursor < 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "cursor cannot be negative"})
		return
	}
	if req.Limit < 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "limit cannot be negative"})
		return
	}
	root, err := api.fileInfo(req.Path)
	if err != nil {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
		return
	}
	if !root.IsDir || root.IsSymlink {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "path must be a directory and not a symlink"})
		return
	}

	queue := &directoryQueue{}
	heap.Init(queue)
	pushChildren := func(path string, level int) error {
		listed, err := listFiles(api.filesystem, path, workspacesdk.LSRequest{})
		if err != nil {
			return err
		}
		for _, entry := range listed.Contents {
			if !req.IncludeHidden && strings.HasPrefix(entry.Name, ".") {
				continue
			}
			info, err := api.fileInfo(entry.AbsolutePathString)
			if err != nil {
				return err
			}
			heap.Push(queue, directoryQueueItem{
				info:    info,
				level:   level,
				sortKey: directoryRelativeKey(req.Path, info.Path),
			})
		}
		return nil
	}
	if err := pushChildren(req.Path, 1); err != nil {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
		return
	}

	entries := make([]workspacesdk.WorkspaceFileInfo, 0)
	legacySeen := 0
	hasMore := false
	for queue.Len() > 0 {
		raw := heap.Pop(queue)
		item, ok := raw.(directoryQueueItem)
		if !ok {
			panic("directoryQueue: unexpected item type")
		}

		eligible := true
		if req.AfterPath != "" {
			eligible = item.sortKey > req.AfterPath
		} else if legacySeen < req.Cursor {
			eligible = false
		}
		legacySeen++

		if eligible && req.Limit > 0 && len(entries) >= req.Limit {
			hasMore = true
			break
		}
		if eligible {
			entries = append(entries, item.info)
		}

		if item.info.IsDir && !item.info.IsSymlink && item.level < req.Depth {
			if err := pushChildren(item.info.Path, item.level+1); err != nil {
				httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
				return
			}
		}
	}

	var next *int
	if hasMore && req.AfterPath == "" {
		value := req.Cursor + len(entries)
		next = &value
	}
	httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.ListDirectoryResponse{
		Entries:    entries,
		NextCursor: next,
		HasMore:    hasMore,
	})
}

func (api *API) HandleFileInfo(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()
	parser := httpapi.NewQueryParamParser().RequiredNotEmpty("path")
	path := parser.String(query, "", "path")
	parser.ErrorExcessParams(query)
	if len(parser.Errors) > 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{
			Message:     "Query parameters have invalid values.",
			Validations: parser.Errors,
		})
		return
	}
	if !filepath.IsAbs(path) {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "path must be absolute"})
		return
	}
	info, err := api.fileInfo(path)
	if err != nil {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, info)
}

func (api *API) HandleCreateDirectory(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req workspacesdk.CreateDirectoryRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if !filepath.IsAbs(req.Path) {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "path must be absolute"})
		return
	}
	var err error
	if req.Parents {
		err = api.filesystem.MkdirAll(req.Path, 0o755)
	} else {
		err = api.filesystem.Mkdir(req.Path, 0o755)
	}
	if err != nil {
		// Treat an already-existing directory as an idempotent success.
		if info, statErr := api.filesystem.Stat(req.Path); statErr != nil || !info.IsDir() {
			httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
			return
		}
	}
	if api.pathStore != nil {
		if chatContext, ok := agentchat.FromContext(ctx); ok {
			api.pathStore.AddPaths(append([]uuid.UUID{chatContext.ID}, chatContext.AncestorIDs...), []string{req.Path})
		}
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.Response{Message: "Directory created."})
}

func (api *API) HandleMoveFile(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req workspacesdk.MoveFileRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if !filepath.IsAbs(req.Source) || !filepath.IsAbs(req.Dest) {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "source and dest must be absolute"})
		return
	}
	req.Source = filepath.Clean(req.Source)
	req.Dest = filepath.Clean(req.Dest)
	if req.Source == req.Dest {
		httpapi.Write(ctx, rw, http.StatusOK, codersdk.Response{Message: "Source and destination are identical."})
		return
	}
	if _, err := api.fileInfo(req.Source); err != nil {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
		return
	}
	destExists := false
	backupPath := ""
	if destInfo, err := api.fileInfo(req.Dest); err == nil {
		destExists = true
		if !req.Overwrite {
			httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{Message: "destination already exists"})
			return
		}
		if destInfo.IsDir && !destInfo.IsSymlink {
			children, readErr := afero.ReadDir(api.filesystem, req.Dest)
			if readErr != nil {
				httpapi.Write(ctx, rw, filesystemStatus(readErr), codersdk.Response{Message: xerrors.Errorf("inspect destination directory: %w", readErr).Error()})
				return
			}
			if len(children) > 0 {
				httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{Message: "destination directory is not empty; overwrite only replaces a removable destination"})
				return
			}
		}
		backupPath = req.Dest + ".coder-backup." + uuid.New().String()
		if err := api.filesystem.Rename(req.Dest, backupPath); err != nil {
			httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: xerrors.Errorf("prepare destination backup: %w", err).Error()})
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
		return
	}
	if err := api.filesystem.Rename(req.Source, req.Dest); err != nil {
		if destExists {
			if rollbackErr := api.filesystem.Rename(backupPath, req.Dest); rollbackErr != nil {
				httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{Message: xerrors.Errorf("move path: %v; destination rollback also failed: %v; backup remains at %s", err, rollbackErr, backupPath).Error()})
				return
			}
		}
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: xerrors.Errorf("move path: %w", err).Error()})
		return
	}
	if destExists {
		if cleanupErr := api.filesystem.Remove(backupPath); cleanupErr != nil {
			// The requested move itself succeeded, but leaving the old destination
			// behind under a hidden backup name is not an acceptable silent side
			// effect. Try to restore the complete pre-call state.
			restoreSourceErr := api.filesystem.Rename(req.Dest, req.Source)
			var restoreDestErr error
			if restoreSourceErr == nil {
				restoreDestErr = api.filesystem.Rename(backupPath, req.Dest)
			}
			if restoreSourceErr == nil && restoreDestErr == nil {
				httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{Message: xerrors.Errorf("move rolled back because destination backup cleanup failed: %w", cleanupErr).Error()})
				return
			}
			httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{Message: xerrors.Errorf(
				"partial move state after destination backup cleanup failed: cleanup=%v; restore_source=%v; restore_destination=%v; source=%s; destination=%s; backup=%s; do not retry blindly",
				cleanupErr, restoreSourceErr, restoreDestErr, req.Source, req.Dest, backupPath,
			).Error()})
			return
		}
	}
	if api.pathStore != nil {
		if chatContext, ok := agentchat.FromContext(ctx); ok {
			ids := append([]uuid.UUID{chatContext.ID}, chatContext.AncestorIDs...)
			api.pathStore.AddPaths(ids, []string{req.Source, req.Dest})
		}
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.Response{Message: "Path moved."})
}
