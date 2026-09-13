package toolsdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"path/filepath"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const maxDirectoryTraversalEntries = 5000

type WorkspaceListDirectoryV2Args struct {
	Workspace     string `json:"workspace"`
	Path          string `json:"path"`
	Depth         int    `json:"depth,omitempty"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
	Cursor        int    `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type WorkspaceDirectoryEntry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	IsDir       bool   `json:"is_dir"`
	IsSymlink   bool   `json:"is_symlink,omitempty"`
	Size        int64  `json:"size"`
	Mode        string `json:"mode,omitempty"`
	ModTimeUnix int64  `json:"mod_time_unix,omitempty"`
}

type WorkspaceListDirectoryV2Result struct {
	Entries    []WorkspaceDirectoryEntry `json:"entries"`
	NextCursor *int                      `json:"next_cursor,omitempty"`
}

var WorkspaceListDirectoryV2 = Tool[WorkspaceListDirectoryV2Args, WorkspaceListDirectoryV2Result]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceListDirectoryV2,
		Description: `List a workspace directory with optional bounded recursion, metadata, hidden-file control, and pagination.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute directory path."},
				"depth": map[string]any{
					"type":        "integer",
					"description": "Directory depth to return. 1 lists direct children only. Defaults to 1, maximum 10.",
					"minimum":     1,
					"maximum":     10,
				},
				"include_hidden": map[string]any{"type": "boolean", "description": "Include entries whose basename starts with a dot."},
				"cursor":         map[string]any{"type": "integer", "description": "Zero-based result cursor. Maximum 5000.", "minimum": 0, "maximum": maxDirectoryTraversalEntries},
				"limit":          map[string]any{"type": "integer", "description": "Maximum entries returned. Defaults to 200, maximum 1000.", "minimum": 1, "maximum": 1000},
			},
			Required: []string{"workspace", "path"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceListDirectoryV2Args) (WorkspaceListDirectoryV2Result, error) {
		if args.Workspace == "" || args.Path == "" {
			return WorkspaceListDirectoryV2Result{}, xerrors.New("workspace and path are required")
		}
		if args.Cursor < 0 || args.Cursor > maxDirectoryTraversalEntries {
			return WorkspaceListDirectoryV2Result{}, xerrors.Errorf("cursor must be between 0 and %d", maxDirectoryTraversalEntries)
		}
		depth := args.Depth
		if depth == 0 {
			depth = 1
		}
		if depth < 1 || depth > 10 {
			return WorkspaceListDirectoryV2Result{}, xerrors.New("depth must be between 1 and 10")
		}
		limit := args.Limit
		if limit == 0 {
			limit = 200
		}
		if limit < 1 || limit > 1000 {
			return WorkspaceListDirectoryV2Result{}, xerrors.New("limit must be between 1 and 1000")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceListDirectoryV2Result{}, err
		}
		defer conn.Close()

		resp, err := conn.ListDirectory(ctx, workspacesdk.ListDirectoryRequest{
			Path:          args.Path,
			Depth:         depth,
			IncludeHidden: args.IncludeHidden,
			Cursor:        args.Cursor,
			Limit:         limit,
		})
		if err != nil {
			return WorkspaceListDirectoryV2Result{}, xerrors.Errorf("list directory: %w", err)
		}
		entries := make([]WorkspaceDirectoryEntry, 0, len(resp.Entries))
		for _, info := range resp.Entries {
			entries = append(entries, WorkspaceDirectoryEntry{
				Path:        info.Path,
				Name:        info.Name,
				IsDir:       info.IsDir,
				IsSymlink:   info.IsSymlink,
				Size:        info.Size,
				Mode:        info.Mode,
				ModTimeUnix: info.ModTimeUnix,
			})
		}
		return WorkspaceListDirectoryV2Result{Entries: entries, NextCursor: resp.NextCursor}, nil
	},
}

type WorkspaceReadFileV2Args struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	Offset    int64  `json:"offset,omitempty"`
	Limit     int64  `json:"limit,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

type WorkspaceReadFileV2Result struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Encoding   string `json:"encoding"`
	MimeType   string `json:"mime_type,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
	TotalLines int    `json:"total_lines,omitempty"`
	LinesRead  int    `json:"lines_read,omitempty"`
	NextOffset int64  `json:"next_offset"`
	EndOfFile  bool   `json:"eof"`
	Error      string `json:"error,omitempty"`
}

func readWorkspaceFileV2(ctx context.Context, conn workspacesdk.AgentConn, args WorkspaceReadFileV2Args) (WorkspaceReadFileV2Result, error) {
	if args.Binary {
		resolvedPath, err := conn.ResolvePath(ctx, args.Path)
		if err != nil {
			return WorkspaceReadFileV2Result{}, xerrors.Errorf("resolve file path: %w", err)
		}
		info, err := conn.FileInfo(ctx, resolvedPath)
		if err != nil {
			return WorkspaceReadFileV2Result{}, err
		}
		if info.IsDir {
			return WorkspaceReadFileV2Result{}, xerrors.Errorf("path %q is a directory", args.Path)
		}
		offset := args.Offset
		if offset < 0 {
			return WorkspaceReadFileV2Result{}, xerrors.New("binary offset cannot be negative")
		}
		limit := args.Limit
		if limit == 0 {
			limit = 64 << 10
		}
		if limit < 1 || limit > maxFileLimit {
			return WorkspaceReadFileV2Result{}, xerrors.Errorf("binary limit must be between 1 and %d bytes", maxFileLimit)
		}
		reader, mimeType, err := conn.ReadFile(ctx, args.Path, offset, limit)
		if err != nil {
			return WorkspaceReadFileV2Result{}, err
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return WorkspaceReadFileV2Result{}, err
		}
		next := offset + int64(len(data))
		return WorkspaceReadFileV2Result{
			Path:       args.Path,
			Content:    base64.StdEncoding.EncodeToString(data),
			Encoding:   "base64",
			MimeType:   mimeType,
			FileSize:   info.Size,
			NextOffset: next,
			EndOfFile:  next >= info.Size,
		}, nil
	}

	offset := args.Offset
	if offset == 0 {
		offset = 1
	}
	if offset < 1 {
		return WorkspaceReadFileV2Result{}, xerrors.New("text offset is a 1-based line number and must be positive")
	}
	limit := args.Limit
	if limit == 0 {
		limit = 200
	}
	if limit < 1 || limit > int64(workspacesdk.DefaultMaxResponseLines) {
		return WorkspaceReadFileV2Result{}, xerrors.Errorf("text limit must be between 1 and %d lines", workspacesdk.DefaultMaxResponseLines)
	}
	resp, err := conn.ReadFileLines(ctx, args.Path, offset, limit, workspacesdk.DefaultReadFileLinesLimits())
	if err != nil {
		return WorkspaceReadFileV2Result{}, err
	}
	if !resp.Success {
		return WorkspaceReadFileV2Result{}, xerrors.New(resp.Error)
	}
	next := offset + int64(resp.LinesRead)
	eof := resp.TotalLines == 0 || next > int64(resp.TotalLines)
	return WorkspaceReadFileV2Result{
		Path:       args.Path,
		Content:    resp.Content,
		Encoding:   "text",
		FileSize:   resp.FileSize,
		TotalLines: resp.TotalLines,
		LinesRead:  resp.LinesRead,
		NextOffset: next,
		EndOfFile:  eof,
	}, nil
}

var WorkspaceReadFileV2 = Tool[WorkspaceReadFileV2Args, WorkspaceReadFileV2Result]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceReadFileV2,
		Description: `Read a workspace file. Text mode is the default and uses 1-based line offsets with line-numbered output. Set binary=true for byte offsets and base64 content.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute file path."},
				"offset":    map[string]any{"type": "integer", "description": "Text: 1-based line number (default 1). Binary: 0-based byte offset (default 0).", "minimum": 0},
				"limit":     map[string]any{"type": "integer", "description": "Text: lines (default 200). Binary: bytes (default 65536, maximum 1 MiB).", "minimum": 1},
				"binary":    map[string]any{"type": "boolean", "description": "Read bytes and return base64 instead of line-numbered text."},
			},
			Required: []string{"workspace", "path"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceReadFileV2Args) (WorkspaceReadFileV2Result, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceReadFileV2Result{}, err
		}
		defer conn.Close()
		return readWorkspaceFileV2(ctx, conn, args)
	},
}

type WorkspaceReadFilesV2Args struct {
	Workspace string                    `json:"workspace"`
	Files     []WorkspaceReadFileV2Args `json:"files"`
}

type WorkspaceReadFilesV2Result struct {
	Files []WorkspaceReadFileV2Result `json:"files"`
}

var WorkspaceReadFilesV2 = Tool[WorkspaceReadFilesV2Args, WorkspaceReadFilesV2Result]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceReadFilesV2,
		Description: `Read multiple workspace files in one call. Each file returns its own result or error; one missing file does not fail the whole batch.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"files": map[string]any{
					"type":        "array",
					"description": "Up to 20 file read specifications.",
					"minItems":    1,
					"maxItems":    20,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":   map[string]any{"type": "string"},
							"offset": map[string]any{"type": "integer", "minimum": 0},
							"limit":  map[string]any{"type": "integer", "minimum": 1},
							"binary": map[string]any{"type": "boolean"},
						},
						"required": []string{"path"},
					},
				},
			},
			Required: []string{"workspace", "files"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceReadFilesV2Args) (WorkspaceReadFilesV2Result, error) {
		if len(args.Files) == 0 || len(args.Files) > 20 {
			return WorkspaceReadFilesV2Result{}, xerrors.New("files must contain between 1 and 20 entries")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceReadFilesV2Result{}, err
		}
		defer conn.Close()
		results := make([]WorkspaceReadFileV2Result, 0, len(args.Files))
		for _, file := range args.Files {
			file.Workspace = args.Workspace
			result, err := readWorkspaceFileV2(ctx, conn, file)
			if err != nil {
				results = append(results, WorkspaceReadFileV2Result{Path: file.Path, Error: err.Error()})
				continue
			}
			results = append(results, result)
		}
		return WorkspaceReadFilesV2Result{Files: results}, nil
	},
}

type WorkspaceWriteFileV2Args struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Encoding  string `json:"encoding,omitempty"`
}

var WorkspaceWriteFileV2 = Tool[WorkspaceWriteFileV2Args, codersdk.Response]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceWriteFileV2,
		Description: `Write a complete workspace file. Content is UTF-8 text by default; set encoding=base64 for binary bytes. This tool replaces the file and never appends.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute file path."},
				"content":   map[string]any{"type": "string", "description": "Text content or base64 according to encoding."},
				"encoding":  map[string]any{"type": "string", "description": "text (default) or base64.", "enum": []string{"text", "base64"}},
			},
			Required: []string{"workspace", "path", "content"},
		},
	},
	MCPAnnotations:     mcpDestructiveAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceWriteFileV2Args) (codersdk.Response, error) {
		encoding := args.Encoding
		if encoding == "" {
			encoding = "text"
		}
		var data []byte
		switch encoding {
		case "text":
			data = []byte(args.Content)
		case "base64":
			decoded, err := base64.StdEncoding.DecodeString(args.Content)
			if err != nil {
				return codersdk.Response{}, xerrors.Errorf("decode base64 content: %w", err)
			}
			data = decoded
		default:
			return codersdk.Response{}, xerrors.New("encoding must be text or base64")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return codersdk.Response{}, err
		}
		defer conn.Close()
		if err := conn.WriteFile(ctx, args.Path, bytes.NewReader(data)); err != nil {
			return codersdk.Response{}, err
		}
		return codersdk.Response{Message: "File written successfully."}, nil
	},
}

type WorkspaceFileInfoArgs struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
}

var WorkspaceFileInfoTool = Tool[WorkspaceFileInfoArgs, workspacesdk.WorkspaceFileInfo]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceFileInfo,
		Description: `Return metadata for a workspace filesystem path without reading its content.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute path."},
			},
			Required: []string{"workspace", "path"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceFileInfoArgs) (workspacesdk.WorkspaceFileInfo, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.WorkspaceFileInfo{}, err
		}
		defer conn.Close()
		return conn.FileInfo(ctx, args.Path)
	},
}

type WorkspaceCreateDirectoryArgs struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	Parents   bool   `json:"parents,omitempty"`
}

var WorkspaceCreateDirectory = Tool[WorkspaceCreateDirectoryArgs, codersdk.Response]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceCreateDirectory,
		Description: `Create a directory in a workspace. Existing directories are treated as success.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute directory path."},
				"parents":   map[string]any{"type": "boolean", "description": "Create missing parent directories."},
			},
			Required: []string{"workspace", "path"},
		},
	},
	MCPAnnotations:     mcpMutationAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceCreateDirectoryArgs) (codersdk.Response, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return codersdk.Response{}, err
		}
		defer conn.Close()
		if err := conn.CreateDirectory(ctx, workspacesdk.CreateDirectoryRequest{Path: args.Path, Parents: args.Parents}); err != nil {
			return codersdk.Response{}, err
		}
		return codersdk.Response{Message: "Directory created."}, nil
	},
}

type WorkspaceMoveFileArgs struct {
	Workspace string `json:"workspace"`
	Source    string `json:"source"`
	Dest      string `json:"dest"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

var WorkspaceMoveFile = Tool[WorkspaceMoveFileArgs, codersdk.Response]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceMoveFile,
		Description: `Move or rename a workspace file or directory without shell quoting. Destination overwrite is disabled by default.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"source":    map[string]any{"type": "string", "description": "Absolute source path."},
				"dest":      map[string]any{"type": "string", "description": "Absolute destination path."},
				"overwrite": map[string]any{"type": "boolean", "description": "Allow replacing an existing removable destination. Defaults to false."},
			},
			Required: []string{"workspace", "source", "dest"},
		},
	},
	MCPAnnotations:     mcpDestructiveAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceMoveFileArgs) (codersdk.Response, error) {
		if filepath.Clean(args.Source) == filepath.Clean(args.Dest) {
			return codersdk.Response{Message: "Source and destination are identical."}, nil
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return codersdk.Response{}, err
		}
		defer conn.Close()
		if err := conn.MoveFile(ctx, workspacesdk.MoveFileRequest{Source: args.Source, Dest: args.Dest, Overwrite: args.Overwrite}); err != nil {
			return codersdk.Response{}, err
		}
		return codersdk.Response{Message: "Path moved."}, nil
	},
}
