package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/buildinfo"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

const (
	// MCPServerName is the name used for the MCP server.
	MCPServerName = "Coder"
	// MCPServerInstructions is the instructions text for the MCP server.
	MCPServerInstructions = "Coder MCP Server providing workspace and template management tools"

	// Used in tests and aibridge.
	MCPEndpoint = "/api/experimental/mcp/http"
)

// Server represents an MCP HTTP server instance
type Server struct {
	Logger slog.Logger

	// mcpServer is the underlying MCP server
	mcpServer *server.MCPServer

	// streamableServer handles HTTP transport
	streamableServer *server.StreamableHTTPServer

	activityStore  *ActivityStore
	activityUserID string
}

// NewServer creates a new MCP HTTP server
func NewServer(logger slog.Logger) (*Server, error) {
	// Create the core MCP server
	mcpSrv := server.NewMCPServer(
		MCPServerName,
		buildinfo.Version(),
		server.WithInstructions(MCPServerInstructions),
	)

	// Create logger adapter for mcp-go
	mcpLogger := &mcpLoggerAdapter{logger: logger}

	// Create streamable HTTP server with configuration
	streamableServer := server.NewStreamableHTTPServer(mcpSrv,
		server.WithHeartbeatInterval(30*time.Second),
		server.WithLogger(mcpLogger),
	)

	return &Server{
		Logger:           logger,
		mcpServer:        mcpSrv,
		streamableServer: streamableServer,
	}, nil
}

// ServeHTTP implements http.Handler interface
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.streamableServer.ServeHTTP(w, r)
}

// SetActivityStore enables bounded, redacted activity metadata for the
// authenticated user. The store is owned by the long-lived HTTP handler so
// records survive individual MCP HTTP requests and session reconnects.
func (s *Server) SetActivityStore(store *ActivityStore, userID string) {
	s.activityStore = store
	s.activityUserID = userID
}

// Register all available MCP tools with the server excluding:
// - ReportTask - which requires dependencies not available in the remote MCP context
// - ChatGPT search and fetch tools, which are redundant with the standard tools.
func (s *Server) RegisterTools(client *codersdk.Client, opts ...func(*toolsdk.Deps)) error {
	if client == nil {
		return xerrors.New("client cannot be nil: MCP HTTP server requires authenticated client")
	}

	// Create tool dependencies
	toolDeps, err := toolsdk.NewDeps(client, opts...)
	if err != nil {
		return xerrors.Errorf("failed to initialize tool dependencies: %w", err)
	}

	for _, tool := range toolsdk.All {
		// the ReportTask tool requires dependencies not available in the remote MCP context
		// the ChatGPT search and fetch tools are redundant with the standard tools.
		if tool.Name == toolsdk.ToolNameReportTask ||
			tool.Name == toolsdk.ToolNameChatGPTSearch || tool.Name == toolsdk.ToolNameChatGPTFetch {
			continue
		}

		s.mcpServer.AddTools(mcpFromSDK(tool, toolDeps))
	}
	return nil
}

type toolAlias struct {
	SDKName string
	MCPName string
}

var developerToolAliases = []toolAlias{
	{SDKName: toolsdk.ToolNameGetWorkspace, MCPName: "status"},
	{SDKName: toolsdk.ToolNameListAccessibleWorkspaces, MCPName: "list_workspaces"},
	{SDKName: toolsdk.ToolNameWorkspaceListDirectoryV2, MCPName: "list_directory"},
	{SDKName: toolsdk.ToolNameWorkspaceReadFileV2, MCPName: "read_file"},
	{SDKName: toolsdk.ToolNameWorkspaceReadFilesV2, MCPName: "read_files"},
	{SDKName: toolsdk.ToolNameWorkspaceWriteFileV2, MCPName: "write_file"},
	{SDKName: toolsdk.ToolNameWorkspaceFileInfo, MCPName: "file_info"},
	{SDKName: toolsdk.ToolNameWorkspaceCreateDirectory, MCPName: "create_directory"},
	{SDKName: toolsdk.ToolNameWorkspaceMoveFile, MCPName: "move_file"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchStart, MCPName: "search_start"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchResults, MCPName: "search_results"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchList, MCPName: "search_list"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchStop, MCPName: "search_stop"},
	{SDKName: toolsdk.ToolNameWorkspaceEditFile, MCPName: "edit_file"},
	{SDKName: toolsdk.ToolNameWorkspaceEditFiles, MCPName: "edit_files"},
	{SDKName: toolsdk.ToolNameWorkspaceBash, MCPName: "bash"},
	{SDKName: toolsdk.ToolNameWorkspaceExec, MCPName: "exec"},
	{SDKName: toolsdk.ToolNameWorkspaceProcessStartV2, MCPName: "process_start"},
	{SDKName: toolsdk.ToolNameWorkspaceProcessOutput, MCPName: "process_output"},
	{SDKName: toolsdk.ToolNameWorkspaceProcessList, MCPName: "process_list"},
	{SDKName: toolsdk.ToolNameWorkspaceProcessInput, MCPName: "process_input"},
	{SDKName: toolsdk.ToolNameWorkspaceProcessSignal, MCPName: "process_signal"},
	{SDKName: toolsdk.ToolNameWorkspaceListApps, MCPName: "list_apps"},
}

var readonlyToolAliases = []toolAlias{
	{SDKName: toolsdk.ToolNameGetWorkspace, MCPName: "status"},
	{SDKName: toolsdk.ToolNameListAccessibleWorkspaces, MCPName: "list_workspaces"},
	{SDKName: toolsdk.ToolNameWorkspaceListDirectoryV2, MCPName: "list_directory"},
	{SDKName: toolsdk.ToolNameWorkspaceReadFileV2, MCPName: "read_file"},
	{SDKName: toolsdk.ToolNameWorkspaceReadFilesV2, MCPName: "read_files"},
	{SDKName: toolsdk.ToolNameWorkspaceFileInfo, MCPName: "file_info"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchStart, MCPName: "search_start"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchResults, MCPName: "search_results"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchList, MCPName: "search_list"},
	{SDKName: toolsdk.ToolNameWorkspaceSearchStop, MCPName: "search_stop"},
	{SDKName: toolsdk.ToolNameWorkspaceProcessOutput, MCPName: "process_output"},
	{SDKName: toolsdk.ToolNameWorkspaceProcessList, MCPName: "process_list"},
	{SDKName: toolsdk.ToolNameWorkspaceListApps, MCPName: "list_apps"},
}

// RegisterDeveloperTools exposes the curated, assistant-facing Remote MCP toolset.
// It reuses the existing tool handlers while publishing concise names that omit
// implementation details such as the Coder and workspace prefixes.
func (s *Server) RegisterDeveloperTools(client *codersdk.Client, opts ...func(*toolsdk.Deps)) error {
	return s.registerAliasedTools(client, developerToolAliases, opts...)
}

// RegisterReadonlyTools exposes only read-only tools from the developer toolset.
func (s *Server) RegisterReadonlyTools(client *codersdk.Client, opts ...func(*toolsdk.Deps)) error {
	return s.registerAliasedTools(client, readonlyToolAliases, opts...)
}

func (s *Server) registerAliasedTools(client *codersdk.Client, aliases []toolAlias, opts ...func(*toolsdk.Deps)) error {
	if client == nil {
		return xerrors.New("client cannot be nil: MCP HTTP server requires authenticated client")
	}

	toolDeps, err := toolsdk.NewDeps(client, opts...)
	if err != nil {
		return xerrors.Errorf("failed to initialize tool dependencies: %w", err)
	}

	toolsByName := make(map[string]toolsdk.GenericTool, len(toolsdk.All)+16)
	for _, tool := range toolsdk.All {
		toolsByName[tool.Name] = tool
	}
	// Assistant-facing tools can evolve independently of the legacy/full Admin
	// catalog. Keep them MCP-only unless they are already part of toolsdk.All.
	toolsByName[toolsdk.ToolNameListAccessibleWorkspaces] = toolsdk.ListAccessibleWorkspaces.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceExec] = toolsdk.WorkspaceExec.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceProcessStartV2] = toolsdk.WorkspaceProcessStartV2.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceProcessInput] = toolsdk.WorkspaceProcessInput.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceListDirectoryV2] = toolsdk.WorkspaceListDirectoryV2.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceReadFileV2] = toolsdk.WorkspaceReadFileV2.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceReadFilesV2] = toolsdk.WorkspaceReadFilesV2.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceWriteFileV2] = toolsdk.WorkspaceWriteFileV2.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceFileInfo] = toolsdk.WorkspaceFileInfoTool.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceCreateDirectory] = toolsdk.WorkspaceCreateDirectory.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceMoveFile] = toolsdk.WorkspaceMoveFile.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceSearchStart] = toolsdk.WorkspaceSearchStart.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceSearchResults] = toolsdk.WorkspaceSearchResults.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceSearchList] = toolsdk.WorkspaceSearchList.Generic()
	toolsByName[toolsdk.ToolNameWorkspaceSearchStop] = toolsdk.WorkspaceSearchStop.Generic()

	replacements := make([]string, 0, len(aliases)*2)
	for _, alias := range aliases {
		replacements = append(replacements, alias.SDKName, alias.MCPName)
	}
	replacer := strings.NewReplacer(replacements...)

	for _, alias := range aliases {
		tool, ok := toolsByName[alias.SDKName]
		if !ok {
			return xerrors.Errorf("MCP tool %q is not registered in toolsdk", alias.SDKName)
		}
		serverTool := mcpFromSDK(tool, toolDeps)
		serverTool.Tool.Name = alias.MCPName
		serverTool.Tool.Description = replacer.Replace(serverTool.Tool.Description)
		serverTool.Tool.InputSchema.Properties = rewriteSchemaStrings(serverTool.Tool.InputSchema.Properties, replacer).(map[string]any)
		rewriteAssistantWorkspaceDescriptions(serverTool.Tool.InputSchema.Properties)
		serverTool = withSharedWorkspaceResolution(serverTool, client)
		serverTool = s.withActivityTracking(serverTool, alias.MCPName)
		s.mcpServer.AddTools(serverTool)
	}
	s.registerRecentActivityTool()
	return nil
}

const (
	assistantWorkspaceDescription      = "The workspace ID or name in the format [owner/]workspace. A bare name first checks the authenticated user's own workspace; if it is not found, a unique accessible shared workspace with that name is used. Use owner/workspace when a name is ambiguous."
	assistantWorkspaceAgentDescription = "The workspace name in the format [owner/]workspace[.agent]. A bare name first checks the authenticated user's own workspace; if it is not found, a unique accessible shared workspace with that name is used. Use owner/workspace when a name is ambiguous."
)

func rewriteAssistantWorkspaceDescriptions(properties map[string]any) {
	for key, description := range map[string]string{
		"workspace_id": assistantWorkspaceDescription,
		"workspace":    assistantWorkspaceAgentDescription,
	} {
		property, ok := properties[key].(map[string]any)
		if !ok {
			continue
		}
		property["description"] = description
	}
}

func withSharedWorkspaceResolution(serverTool server.ServerTool, client *codersdk.Client) server.ServerTool {
	originalHandler := serverTool.Handler
	serverTool.Handler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := originalHandler(ctx, request)
		if err == nil || !isNotFoundError(err) {
			return result, err
		}

		field, workspaceInput, hasAgent, ok := workspaceArgument(request)
		if !ok {
			return nil, err
		}

		resolved, resolveErr := resolveAccessibleSharedWorkspace(ctx, client, workspaceInput, hasAgent)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved == "" {
			return nil, err
		}

		arguments := request.GetArguments()
		cloned := make(map[string]any, len(arguments))
		for key, value := range arguments {
			cloned[key] = value
		}
		cloned[field] = resolved
		request.Params.Arguments = cloned
		return originalHandler(ctx, request)
	}
	return serverTool
}

func workspaceArgument(request mcp.CallToolRequest) (field, value string, hasAgent, ok bool) {
	arguments := request.GetArguments()
	if workspaceID, exists := arguments["workspace_id"].(string); exists && workspaceID != "" {
		return "workspace_id", workspaceID, false, true
	}
	if workspace, exists := arguments["workspace"].(string); exists && workspace != "" {
		return "workspace", workspace, true, true
	}
	return "", "", false, false
}

func resolveAccessibleSharedWorkspace(ctx context.Context, client *codersdk.Client, input string, hasAgent bool) (string, error) {
	normalized := toolsdk.NormalizeWorkspaceInput(input)
	workspaceName := normalized
	agentSuffix := ""
	if hasAgent {
		if workspace, agent, found := strings.Cut(normalized, "."); found {
			workspaceName = workspace
			agentSuffix = "." + agent
		}
	}

	// Explicit owner-qualified names already identify the intended workspace.
	if strings.Contains(workspaceName, "/") {
		return "", nil
	}

	visible, err := client.Workspaces(ctx, codersdk.WorkspaceFilter{})
	if err != nil {
		return "", xerrors.Errorf("list accessible workspaces: %w", err)
	}

	matches := make([]codersdk.Workspace, 0, 1)
	matchNames := make([]string, 0, 1)
	for _, candidate := range visible.Workspaces {
		if !strings.EqualFold(candidate.Name, workspaceName) {
			continue
		}
		matches = append(matches, candidate)
		matchNames = append(matchNames, candidate.OwnerName+"/"+candidate.Name)
	}

	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0].OwnerName + "/" + matches[0].Name + agentSuffix, nil
	default:
		sort.Strings(matchNames)
		return "", xerrors.Errorf(
			"workspace name %q is ambiguous; use owner/workspace. Accessible matches: %s",
			workspaceName, strings.Join(matchNames, ", "),
		)
	}
}

func isNotFoundError(err error) bool {
	var sdkErr *codersdk.Error
	return errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound
}

func rewriteSchemaStrings(value any, replacer *strings.Replacer) any {
	switch value := value.(type) {
	case string:
		return replacer.Replace(value)
	case map[string]any:
		cloned := make(map[string]any, len(value))
		for key, item := range value {
			cloned[key] = rewriteSchemaStrings(item, replacer)
		}
		return cloned
	case []any:
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = rewriteSchemaStrings(item, replacer)
		}
		return cloned
	default:
		return value
	}
}

// ChatGPT tools are the search and fetch tools as defined in https://platform.openai.com/docs/mcp.
// We do not expose any extra ones because ChatGPT has an undocumented "Safety Scan" feature.
// In my experiments, if I included extra tools in the MCP server, ChatGPT would often - but not always -
// refuse to add Coder as a connector.
func (s *Server) RegisterChatGPTTools(client *codersdk.Client, opts ...func(*toolsdk.Deps)) error {
	if client == nil {
		return xerrors.New("client cannot be nil: MCP HTTP server requires authenticated client")
	}

	// Create tool dependencies
	toolDeps, err := toolsdk.NewDeps(client, opts...)
	if err != nil {
		return xerrors.Errorf("failed to initialize tool dependencies: %w", err)
	}

	for _, tool := range toolsdk.All {
		if tool.Name != toolsdk.ToolNameChatGPTSearch && tool.Name != toolsdk.ToolNameChatGPTFetch {
			continue
		}

		s.mcpServer.AddTools(mcpFromSDK(tool, toolDeps))
	}
	return nil
}

// mcpFromSDK adapts a toolsdk.Tool to go-mcp's server.ServerTool
func mcpFromSDK(sdkTool toolsdk.GenericTool, tb toolsdk.Deps) server.ServerTool {
	if sdkTool.Schema.Properties == nil {
		panic("developer error: schema properties cannot be nil")
	}

	return server.ServerTool{
		Tool: mcp.Tool{
			Name:        sdkTool.Name,
			Description: sdkTool.Description,
			InputSchema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: sdkTool.Schema.Properties,
				Required:   sdkTool.Schema.Required,
			},
			Annotations: mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(sdkTool.MCPAnnotations.ReadOnlyHint),
				DestructiveHint: mcp.ToBoolPtr(sdkTool.MCPAnnotations.DestructiveHint),
				IdempotentHint:  mcp.ToBoolPtr(sdkTool.MCPAnnotations.IdempotentHint),
				OpenWorldHint:   mcp.ToBoolPtr(sdkTool.MCPAnnotations.OpenWorldHint),
			},
		},
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var buf bytes.Buffer
			if err := json.NewEncoder(&buf).Encode(request.Params.Arguments); err != nil {
				return nil, xerrors.Errorf("failed to encode request arguments: %w", err)
			}
			result, err := sdkTool.Handler(ctx, tb, buf.Bytes())
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.NewTextContent(string(result)),
				},
			}, nil
		},
	}
}

// mcpLoggerAdapter adapts slog.Logger to the mcp-go util.Logger interface
type mcpLoggerAdapter struct {
	logger slog.Logger
}

func (l *mcpLoggerAdapter) Infof(format string, v ...any) {
	l.logger.Info(context.Background(), fmt.Sprintf(format, v...))
}

func (l *mcpLoggerAdapter) Errorf(format string, v ...any) {
	l.logger.Error(context.Background(), fmt.Sprintf(format, v...))
}
