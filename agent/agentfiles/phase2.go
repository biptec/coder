package agentfiles

import (
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

const maxListDirectoryEntries = 5000

var errDirectoryPageFull = errors.New("directory page full")

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
	if req.Depth < 1 || req.Depth > 10 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "depth must be between 1 and 10"})
		return
	}
	if req.Cursor < 0 || req.Cursor > maxListDirectoryEntries {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "cursor must be between 0 and 5000"})
		return
	}
	if req.Limit == 0 {
		req.Limit = 200
	}
	if req.Limit < 1 || req.Limit > 1000 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "limit must be between 1 and 1000"})
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

	entries := make([]workspacesdk.WorkspaceFileInfo, 0, req.Limit)
	seen := 0
	hasMore := false
	var walk func(string, int) error
	walk = func(path string, level int) error {
		listed, err := listFiles(api.filesystem, path, workspacesdk.LSRequest{})
		if err != nil {
			return err
		}
		for _, entry := range listed.Contents {
			if !req.IncludeHidden && strings.HasPrefix(entry.Name, ".") {
				continue
			}
			if seen >= maxListDirectoryEntries {
				return xerrors.Errorf("directory traversal exceeds %d entries; reduce depth or narrow the path", maxListDirectoryEntries)
			}
			info, err := api.fileInfo(entry.AbsolutePathString)
			if err != nil {
				return err
			}
			if seen >= req.Cursor {
				if len(entries) >= req.Limit {
					hasMore = true
					return errDirectoryPageFull
				}
				entries = append(entries, info)
			}
			seen++
			if info.IsDir && !info.IsSymlink && level < req.Depth {
				if err := walk(info.Path, level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	err = walk(req.Path, 1)
	if err != nil && !errors.Is(err, errDirectoryPageFull) {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
		return
	}
	var next *int
	if hasMore {
		value := req.Cursor + len(entries)
		next = &value
	}
	httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.ListDirectoryResponse{
		Entries:    entries,
		NextCursor: next,
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
	if _, err := api.fileInfo(req.Dest); err == nil {
		if !req.Overwrite {
			httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{Message: "destination already exists"})
			return
		}
		if err := api.filesystem.Remove(req.Dest); err != nil {
			httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: xerrors.Errorf("remove destination: %w", err).Error()})
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: err.Error()})
		return
	}
	if err := api.filesystem.Rename(req.Source, req.Dest); err != nil {
		httpapi.Write(ctx, rw, filesystemStatus(err), codersdk.Response{Message: xerrors.Errorf("move path: %w", err).Error()})
		return
	}
	if api.pathStore != nil {
		if chatContext, ok := agentchat.FromContext(ctx); ok {
			ids := append([]uuid.UUID{chatContext.ID}, chatContext.AncestorIDs...)
			api.pathStore.AddPaths(ids, []string{req.Source, req.Dest})
		}
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.Response{Message: "Path moved."})
}
