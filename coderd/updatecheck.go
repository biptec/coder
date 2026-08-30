package coderd

import (
	"database/sql"
	"net/http"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/buildinfo"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
)

// @Summary Update check
// @ID update-check
// @Produce json
// @Tags General
// @Success 200 {object} codersdk.UpdateCheckResponse
// @Router /api/v2/updatecheck [get]
func (api *API) updateCheck(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	currentVersion := codersdk.UpdateCheckResponse{
		Current: true,
		Version: buildinfo.Version(),
		URL:     buildinfo.ExternalURL(),
	}

	if api.updateChecker == nil {
		// If update checking is disabled, echo the current
		// version.
		httpapi.Write(ctx, rw, http.StatusOK, currentVersion)
		return
	}

	uc, err := api.updateChecker.Latest(ctx)
	if err != nil {
		if xerrors.Is(err, sql.ErrNoRows) {
			// Update checking is enabled, but has never
			// succeeded, reproduce behavior as if disabled.
			httpapi.Write(ctx, rw, http.StatusOK, currentVersion)
			return
		}

		httpapi.InternalServerError(rw, err)
		return
	}

	current := buildinfo.Version()
	// Preserve the existing update-check behavior for development builds: a
	// development build of the current upstream version is considered current.
	if buildinfo.IsDevVersion(current) {
		current = strings.SplitN(current, "-", 2)[0]
	}

	httpapi.Write(ctx, rw, http.StatusOK, codersdk.UpdateCheckResponse{
		Current: buildinfo.CompareVersions(current, uc.Version) >= 0,
		Version: uc.Version,
		URL:     uc.URL,
	})
}
