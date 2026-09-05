package coderd

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/mcp"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type MCPToolset string

const (
	MCPToolsetStandard MCPToolset = "standard"
	MCPToolsetChatGPT  MCPToolset = "chatgpt"
)

// mcpHTTPHandler creates the MCP HTTP transport handler
// It supports a "toolset" query parameter to select the set of tools to register.
func (api *API) mcpHTTPHandler() http.Handler {
	activityStore := mcp.NewActivityStore(100)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create MCP server instance for each request
		mcpServer, err := mcp.NewServer(api.Logger.Named("mcp"))
		if err != nil {
			api.Logger.Error(r.Context(), "failed to create MCP server", slog.Error(err))
			httpapi.Write(r.Context(), w, http.StatusInternalServerError, codersdk.Response{
				Message: "MCP server initialization failed",
			})
			return
		}
		// Extract the original session token from the request.
		authenticatedClient := codersdk.New(api.AccessURL,
			codersdk.WithSessionToken(httpmw.APITokenFromRequest(r)))
		mcpServer.SetActivityStore(activityStore, httpmw.APIKey(r).UserID.String())

		// Wrap the agent connection function to enforce ActionSSH
		// on the workspace. Without this check, a user who can read
		// a workspace but lacks SSH permission could still execute
		// commands through MCP tools.
		toolOpt := toolsdk.WithAgentConnFunc(func(ctx context.Context, agentID uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			if api.Entitlements.Enabled(codersdk.FeatureBrowserOnly) {
				return nil, nil, xerrors.New("non-browser connections are disabled")
			}
			// Use system context for the lookup because the tool
			// handler context does not carry a dbauthz actor. The
			// real authorization happens in the Authorize call below.
			//nolint:gocritic // The system query only fetches the workspace
			// object so we can perform an ActionSSH check against it
			// with the real user's roles via api.Authorize.
			workspace, err := api.Database.GetWorkspaceByAgentID(dbauthz.AsSystemRestricted(ctx), agentID)
			if err != nil {
				return nil, nil, xerrors.Errorf("get workspace by agent ID: %w", err)
			}
			// Enforce the same ActionSSH check that the coordinate
			// endpoint uses (workspaceagents.go:1317).
			if !api.Authorize(r, policy.ActionSSH, workspace) {
				return nil, nil, xerrors.New("unauthorized: you do not have SSH access to this workspace")
			}
			return api.agentProvider.AgentConn(ctx, agentID)
		})

		requestedToolset := MCPToolset(r.URL.Query().Get("toolset"))
		// The standard Remote MCP endpoint uses the server-side toolset assigned
		// to the authenticated user. Clients cannot select a more privileged
		// developer/admin/readonly toolset through the query string.
		if requestedToolset == "" {
			requestedToolset = MCPToolsetStandard
		}

		switch requestedToolset {
		case MCPToolsetStandard:
			assignedToolset, err := api.Database.GetUserMCPToolset(r.Context(), httpmw.APIKey(r).UserID)
			if err != nil {
				api.Logger.Error(r.Context(), "failed to resolve MCP toolset", slog.Error(err))
				httpapi.Write(r.Context(), w, http.StatusInternalServerError, codersdk.Response{
					Message: "Failed to resolve MCP toolset.",
				})
				return
			}

			var registerErr error
			switch codersdk.MCPToolset(assignedToolset) {
			case codersdk.MCPToolsetAdmin:
				registerErr = mcpServer.RegisterTools(authenticatedClient, toolOpt)
			case codersdk.MCPToolsetReadonly:
				registerErr = mcpServer.RegisterReadonlyTools(authenticatedClient, toolOpt)
			case codersdk.MCPToolsetDeveloper:
				registerErr = mcpServer.RegisterDeveloperTools(authenticatedClient, toolOpt)
			default:
				// Fail safe if an invalid value somehow reaches the database.
				api.Logger.Warn(r.Context(), "invalid stored MCP toolset; falling back to developer",
					slog.F("toolset", assignedToolset),
				)
				registerErr = mcpServer.RegisterDeveloperTools(authenticatedClient, toolOpt)
			}
			if registerErr != nil {
				api.Logger.Warn(r.Context(), "failed to register MCP tools", slog.Error(registerErr))
			}
		case MCPToolsetChatGPT:
			if err := mcpServer.RegisterChatGPTTools(authenticatedClient, toolOpt); err != nil {
				api.Logger.Warn(r.Context(), "failed to register MCP tools", slog.Error(err))
			}
		default:
			httpapi.Write(r.Context(), w, http.StatusBadRequest, codersdk.Response{
				Message: fmt.Sprintf("Invalid toolset: %s", requestedToolset),
			})
			return
		}

		// Handle the MCP request
		mcpServer.ServeHTTP(w, r)
	})
}
