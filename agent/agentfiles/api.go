package agentfiles

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/afero"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/agent/agentgit"
)

// API exposes file-related operations performed through the agent.
type API struct {
	logger     slog.Logger
	filesystem afero.Fs
	pathStore  *agentgit.PathStore
	searches   *searchManager
}

func NewAPI(logger slog.Logger, filesystem afero.Fs, pathStore *agentgit.PathStore) *API {
	api := &API{
		logger:     logger,
		filesystem: filesystem,
		pathStore:  pathStore,
		searches:   newSearchManager(filesystem),
	}
	return api
}

// Routes returns the HTTP handler for file-related routes.
func (api *API) Routes() http.Handler {
	r := chi.NewRouter()

	r.Post("/list-directory", api.HandleLS)
	r.Post("/list-directory-v2", api.HandleListDirectoryV2)
	r.Get("/resolve-path", api.HandleResolvePath)
	r.Get("/read-file", api.HandleReadFile)
	r.Get("/read-file-lines", api.HandleReadFileLines)
	r.Get("/file-info", api.HandleFileInfo)
	r.Post("/create-directory", api.HandleCreateDirectory)
	r.Post("/move-file", api.HandleMoveFile)
	r.Post("/search/start", api.HandleSearchStart)
	r.Get("/search/list", api.HandleSearchList)
	r.Get("/search/{id}/results", api.HandleSearchResults)
	r.Post("/search/{id}/stop", api.HandleSearchStop)
	r.Post("/write-file", api.HandleWriteFile)
	r.Post("/edit-files", api.HandleEditFiles)

	return r
}
