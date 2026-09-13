package toolsdk

import (
	"context"
	"encoding/base64"
	"path"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceCodeQueryArgs struct {
	Workspace string `json:"workspace"`
	Operation string `json:"operation"`
	Path      string `json:"path,omitempty"`
	Root      string `json:"root,omitempty"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	Query     string `json:"query,omitempty"`
}

const maxCodeToolOutputBytes = 512 << 10

type WorkspaceCodeQueryResult struct {
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
}

var WorkspaceCodeQuery = Tool[WorkspaceCodeQueryArgs, WorkspaceCodeQueryResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceCodeQuery,
		Description: `Query semantic Go code intelligence using the gopls language server CLI available in the workspace.

Supported operations:
- symbols: list symbols in path
- workspace_symbols: search symbols by query, using root as the working directory when provided
- definition, references, implementation, call_hierarchy, signature: query the identifier at path:line:column
- diagnostics: report diagnostics for path

This tool is semantic and read-only. Use search_start for plain text or filename search.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"operation": map[string]any{
					"type":        "string",
					"enum":        []string{"symbols", "workspace_symbols", "definition", "references", "implementation", "diagnostics", "call_hierarchy", "signature"},
					"description": "Semantic code query to perform.",
				},
				"path":   map[string]any{"type": "string", "description": "Absolute source file path. Required by all operations except workspace_symbols."},
				"root":   map[string]any{"type": "string", "description": "Optional absolute working directory, primarily for workspace_symbols."},
				"line":   map[string]any{"type": "integer", "minimum": 1, "description": "1-based line for position-based operations."},
				"column": map[string]any{"type": "integer", "minimum": 1, "description": "1-based column for position-based operations."},
				"query":  map[string]any{"type": "string", "description": "Symbol query required by workspace_symbols."},
			},
			Required: []string{"workspace", "operation"},
		},
	},
	MCPAnnotations:     mcpReadOnlyOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceCodeQueryArgs) (WorkspaceCodeQueryResult, error) {
		argv, workdir, err := codeQueryCommand(args)
		if err != nil {
			return WorkspaceCodeQueryResult{}, err
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceCodeQueryResult{}, err
		}
		defer conn.Close()
		resp, err := conn.RunCommand(ctx, workspacesdk.RunCommandRequest{
			Argv:    argv,
			WorkDir: workdir,
		})
		if err != nil {
			return WorkspaceCodeQueryResult{}, err
		}
		output, decodeErr := base64.StdEncoding.DecodeString(resp.StdoutBase64)
		if decodeErr != nil {
			return WorkspaceCodeQueryResult{}, xerrors.Errorf("decode gopls output: %w", decodeErr)
		}
		if resp.ExitCode != 0 {
			detail := strings.TrimSpace(resp.Stderr)
			if len(output) > 0 {
				outputText, _ := truncateSemanticOutput(output, maxCodeToolOutputBytes)
				detail = strings.TrimSpace(outputText) + "\n" + detail
			}
			return WorkspaceCodeQueryResult{}, xerrors.Errorf("gopls %s failed with exit code %d: %s", args.Operation, resp.ExitCode, strings.TrimSpace(detail))
		}
		outputText, truncated := truncateSemanticOutput(output, maxCodeToolOutputBytes)
		return WorkspaceCodeQueryResult{Output: outputText, Truncated: truncated}, nil
	},
}

func codeQueryCommand(args WorkspaceCodeQueryArgs) ([]string, string, error) {
	operation := strings.TrimSpace(args.Operation)
	positionOperations := map[string]bool{
		"definition": true, "references": true, "implementation": true,
		"call_hierarchy": true, "signature": true,
	}
	if operation == "workspace_symbols" {
		if strings.TrimSpace(args.Query) == "" {
			return nil, "", xerrors.New("query is required for workspace_symbols")
		}
		workdir := args.Root
		if workdir == "" {
			workdir = args.Path
		}
		if workdir != "" && !path.IsAbs(workdir) {
			return nil, "", xerrors.New("root must be absolute")
		}
		return []string{"gopls", "workspace_symbol", args.Query}, workdir, nil
	}
	if args.Path == "" || !path.IsAbs(args.Path) {
		return nil, "", xerrors.New("path must be an absolute source file path")
	}
	workdir := args.Root
	if workdir == "" {
		workdir = path.Dir(args.Path)
	}
	if !path.IsAbs(workdir) {
		return nil, "", xerrors.New("root must be absolute")
	}
	switch operation {
	case "symbols":
		return []string{"gopls", "symbols", args.Path}, workdir, nil
	case "diagnostics":
		return []string{"gopls", "check", args.Path}, workdir, nil
	default:
		if !positionOperations[operation] {
			return nil, "", xerrors.Errorf("unsupported code query operation %q", operation)
		}
		if args.Line < 1 || args.Column < 1 {
			return nil, "", xerrors.New("line and column must be at least 1 for this operation")
		}
		position := args.Path + ":" + strconv.Itoa(args.Line) + ":" + strconv.Itoa(args.Column)
		return []string{"gopls", operation, position}, workdir, nil
	}
}

type WorkspaceCodeRenameArgs struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	NewName   string `json:"new_name"`
	DryRun    bool   `json:"dry_run,omitempty"`
}

type WorkspaceCodeRenameResult struct {
	Output    string `json:"output"`
	DryRun    bool   `json:"dry_run"`
	Truncated bool   `json:"truncated,omitempty"`
}

var WorkspaceCodeRename = Tool[WorkspaceCodeRenameArgs, WorkspaceCodeRenameResult]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceCodeRename,
		Description: `Rename a Go identifier semantically using gopls. The rename can span multiple files. Set dry_run=true to return the proposed diff without writing files.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute source file path containing the identifier."},
				"line":      map[string]any{"type": "integer", "minimum": 1, "description": "1-based line of the identifier."},
				"column":    map[string]any{"type": "integer", "minimum": 1, "description": "1-based column of the identifier."},
				"new_name":  map[string]any{"type": "string", "description": "New identifier name."},
				"dry_run":   map[string]any{"type": "boolean", "description": "Return the proposed diff without changing files."},
			},
			Required: []string{"workspace", "path", "line", "column", "new_name"},
		},
	},
	MCPAnnotations:     mcpDestructiveOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceCodeRenameArgs) (WorkspaceCodeRenameResult, error) {
		if args.Path == "" || !path.IsAbs(args.Path) {
			return WorkspaceCodeRenameResult{}, xerrors.New("path must be an absolute source file path")
		}
		if args.Line < 1 || args.Column < 1 {
			return WorkspaceCodeRenameResult{}, xerrors.New("line and column must be at least 1")
		}
		if strings.TrimSpace(args.NewName) == "" {
			return WorkspaceCodeRenameResult{}, xerrors.New("new_name cannot be empty")
		}
		position := args.Path + ":" + strconv.Itoa(args.Line) + ":" + strconv.Itoa(args.Column)
		argv := []string{"gopls", "rename"}
		if args.DryRun {
			argv = append(argv, "-d")
		} else {
			argv = append(argv, "-w", "-l")
		}
		argv = append(argv, position, args.NewName)

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceCodeRenameResult{}, err
		}
		defer conn.Close()
		resp, err := conn.RunCommand(ctx, workspacesdk.RunCommandRequest{
			Argv:    argv,
			WorkDir: path.Dir(args.Path),
		})
		if err != nil {
			return WorkspaceCodeRenameResult{}, err
		}
		output, decodeErr := base64.StdEncoding.DecodeString(resp.StdoutBase64)
		if decodeErr != nil {
			return WorkspaceCodeRenameResult{}, xerrors.Errorf("decode gopls output: %w", decodeErr)
		}
		if resp.ExitCode != 0 {
			detail := strings.TrimSpace(resp.Stderr)
			if len(output) > 0 {
				outputText, _ := truncateSemanticOutput(output, maxCodeToolOutputBytes)
				detail = strings.TrimSpace(outputText) + "\n" + detail
			}
			return WorkspaceCodeRenameResult{}, xerrors.Errorf("gopls rename failed with exit code %d: %s", resp.ExitCode, strings.TrimSpace(detail))
		}
		outputText, truncated := truncateSemanticOutput(output, maxCodeToolOutputBytes)
		return WorkspaceCodeRenameResult{Output: outputText, DryRun: args.DryRun, Truncated: truncated}, nil
	},
}
