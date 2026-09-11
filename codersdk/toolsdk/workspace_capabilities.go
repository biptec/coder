package toolsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"golang.org/x/xerrors"
)

const (
	workspaceCapabilitiesPath     = "/etc/developer-workspace/capabilities.json"
	maxWorkspaceCapabilitiesBytes = 1 << 20
)

type WorkspaceCapabilitiesArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace name in the format [owner/]workspace[.agent]."`
}

type WorkspaceCapabilitiesResult struct {
	Available bool            `json:"available"`
	Manifest  json.RawMessage `json:"manifest,omitempty"`
	Message   string          `json:"message,omitempty"`
}

var WorkspaceCapabilities = Tool[WorkspaceCapabilitiesArgs, WorkspaceCapabilitiesResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceCapabilities,
		Description: `Inspect the development capabilities preinstalled in a workspace image.

Call this before installing software when beginning substantial work in a workspace or when you need to know whether a dependency is already available. Normally inspect capabilities once per workspace and refresh them only after the workspace environment changes. The returned manifest is generated from the finished image and may evolve as the image gains or updates capabilities.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": workspaceAgentDescription,
				},
			},
			Required: []string{"workspace"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceCapabilitiesArgs) (WorkspaceCapabilitiesResult, error) {
		if args.Workspace == "" {
			return WorkspaceCapabilitiesResult{}, xerrors.New("workspace is required")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceCapabilitiesResult{}, err
		}
		defer conn.Close()
		return readWorkspaceCapabilities(ctx, conn)
	},
}

func readWorkspaceCapabilities(ctx context.Context, conn workspacesdk.AgentConn) (WorkspaceCapabilitiesResult, error) {
	info, err := conn.FileInfo(ctx, workspaceCapabilitiesPath)
	if err != nil {
		if isWorkspaceCapabilitiesNotFound(err) {
			return WorkspaceCapabilitiesResult{
				Available: false,
				Message:   "This workspace image does not publish a capabilities manifest.",
			}, nil
		}
		return WorkspaceCapabilitiesResult{}, xerrors.Errorf("inspect workspace capabilities: %w", err)
	}
	if info.Size > maxWorkspaceCapabilitiesBytes {
		return WorkspaceCapabilitiesResult{}, xerrors.Errorf("workspace capabilities manifest exceeds %d bytes", maxWorkspaceCapabilitiesBytes)
	}

	reader, _, err := conn.ReadFile(ctx, workspaceCapabilitiesPath, 0, maxWorkspaceCapabilitiesBytes)
	if err != nil {
		if isWorkspaceCapabilitiesNotFound(err) {
			return WorkspaceCapabilitiesResult{
				Available: false,
				Message:   "This workspace image does not publish a capabilities manifest.",
			}, nil
		}
		return WorkspaceCapabilitiesResult{}, xerrors.Errorf("read workspace capabilities: %w", err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		return WorkspaceCapabilitiesResult{}, xerrors.Errorf("read workspace capabilities: %w", err)
	}
	if !json.Valid(payload) {
		return WorkspaceCapabilitiesResult{}, xerrors.New("workspace capabilities manifest is not valid JSON")
	}
	return WorkspaceCapabilitiesResult{Available: true, Manifest: json.RawMessage(payload)}, nil
}

func isWorkspaceCapabilitiesNotFound(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var sdkErr *codersdk.Error
	return errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound
}
