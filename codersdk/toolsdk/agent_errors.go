package toolsdk

import (
	"errors"
	"net/http"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk"
)

const outdatedWorkspaceAgentToolMessage = "workspace Agent is outdated and does not support this tool; restart the workspace to update the Agent, then retry"

func workspaceAgentToolError(err error) error {
	if err == nil {
		return nil
	}

	var sdkErr *codersdk.Error
	if !errors.As(err, &sdkErr) || sdkErr.StatusCode() != http.StatusNotFound {
		return err
	}

	// A route that does not exist on an older workspace Agent is served by the
	// Agent mux as a plain-text 404. Resource-level errors from implemented
	// endpoints are JSON codersdk responses and must retain their original
	// semantics (for example, a missing write_file parent directory).
	detail := strings.TrimSpace(strings.ToLower(sdkErr.Detail))
	if detail != "404 page not found" {
		return err
	}
	return xerrors.New(outdatedWorkspaceAgentToolMessage)
}
