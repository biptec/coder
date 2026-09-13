package toolsdk

import (
	"context"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceRemoteHostsArgs struct {
	Workspace string `json:"workspace"`
}

var WorkspaceRemoteHosts = Tool[WorkspaceRemoteHostsArgs, workspacesdk.ListRemoteHostsResponse]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceRemoteHosts,
		Description: `List exact SSH aliases configured in the workspace user's SSH config for use with remote-capable tools.

Only aliases returned by this tool are accepted by host parameters. Wildcard-only Host patterns, raw hostnames/IPs, and user@host targets are intentionally excluded. Configure credentials such as User and IdentityFile in SSH config rather than passing secrets through MCP tool arguments.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
			},
			Required: []string{"workspace"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceRemoteHostsArgs) (workspacesdk.ListRemoteHostsResponse, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.ListRemoteHostsResponse{}, err
		}
		defer conn.Close()
		return conn.ListRemoteHosts(ctx)
	},
}
