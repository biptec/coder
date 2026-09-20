package toolsdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceListDirectoryV2Args struct {
	Workspace     string `json:"workspace"`
	Path          string `json:"path"`
	Depth         int    `json:"depth,omitempty"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
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
	NextCursor string                    `json:"next_cursor,omitempty"`
}

func directorySortKey(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func encodeDirectoryCursor(sortKey string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + sortKey))
}

func decodeDirectoryCursor(cursor string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", xerrors.New("invalid directory cursor")
	}
	value := string(decoded)
	if !strings.HasPrefix(value, "v1:") {
		return "", xerrors.New("invalid directory cursor")
	}
	return strings.TrimPrefix(value, "v1:"), nil
}

var WorkspaceListDirectoryV2 = Tool[WorkspaceListDirectoryV2Args, WorkspaceListDirectoryV2Result]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceListDirectoryV2,
		Description: `List a workspace directory with optional recursion, metadata, hidden-file control, deterministic alphabetical ordering, and assistant-controlled pagination.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute directory path."},
				"depth": map[string]any{
					"type":        "integer",
					"description": "Directory depth to return. 1 lists direct children only. Defaults to 1.",
					"minimum":     1,
				},
				"include_hidden": map[string]any{"type": "boolean", "description": "Include entries whose basename starts with a dot."},
				"cursor":         map[string]any{"type": "string", "description": "Opaque alphabetical continuation cursor returned by a previous limited call.", "minLength": 1},
				"limit":          map[string]any{"type": "integer", "description": "Optional maximum entries returned. If omitted, return the complete directory result.", "minimum": 1},
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
		if args.Limit < 0 {
			return WorkspaceListDirectoryV2Result{}, xerrors.New("limit cannot be negative")
		}
		depth := args.Depth
		if depth == 0 {
			depth = 1
		}
		if depth < 1 {
			return WorkspaceListDirectoryV2Result{}, xerrors.New("depth must be positive")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceListDirectoryV2Result{}, err
		}
		defer conn.Close()

		cursorKey := ""
		if args.Cursor != "" {
			cursorKey, err = decodeDirectoryCursor(args.Cursor)
			if err != nil {
				return WorkspaceListDirectoryV2Result{}, err
			}
		}

		resp, err := conn.ListDirectory(ctx, workspacesdk.ListDirectoryRequest{
			Path:          args.Path,
			Depth:         depth,
			IncludeHidden: args.IncludeHidden,
		})
		if err != nil {
			return WorkspaceListDirectoryV2Result{}, xerrors.Errorf("list directory: %w", err)
		}
		sort.Slice(resp.Entries, func(i, j int) bool {
			return directorySortKey(args.Path, resp.Entries[i].Path) < directorySortKey(args.Path, resp.Entries[j].Path)
		})

		eligible := make([]workspacesdk.WorkspaceFileInfo, 0, len(resp.Entries))
		for _, info := range resp.Entries {
			if cursorKey != "" && directorySortKey(args.Path, info.Path) <= cursorKey {
				continue
			}
			eligible = append(eligible, info)
		}
		hasMore := args.Limit > 0 && len(eligible) > args.Limit
		if hasMore {
			eligible = eligible[:args.Limit]
		}
		entries := make([]WorkspaceDirectoryEntry, 0, len(eligible))
		for _, info := range eligible {
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
		result := WorkspaceListDirectoryV2Result{Entries: entries}
		if hasMore && len(eligible) > 0 {
			result.NextCursor = encodeDirectoryCursor(directorySortKey(args.Path, eligible[len(eligible)-1].Path))
		}
		return result, nil
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

	if args.Binary {
		if args.Offset < 0 {
			return WorkspaceReadFileV2Result{}, xerrors.New("binary offset cannot be negative")
		}
		if args.Limit < 0 {
			return WorkspaceReadFileV2Result{}, xerrors.New("binary limit cannot be negative")
		}
		reader, mimeType, err := conn.ReadFile(ctx, args.Path, args.Offset, args.Limit)
		if err != nil {
			return WorkspaceReadFileV2Result{}, err
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return WorkspaceReadFileV2Result{}, err
		}
		next := args.Offset + int64(len(data))
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
	if args.Limit < 0 {
		return WorkspaceReadFileV2Result{}, xerrors.New("text limit cannot be negative")
	}

	reader, mimeType, err := conn.ReadFile(ctx, args.Path, 0, 0)
	if err != nil {
		return WorkspaceReadFileV2Result{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return WorkspaceReadFileV2Result{}, err
	}

	var lines []string
	if len(data) > 0 {
		lines = strings.SplitAfter(string(data), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	totalLines := len(lines)
	if totalLines == 0 {
		return WorkspaceReadFileV2Result{
			Path:       args.Path,
			Content:    "",
			Encoding:   "text",
			MimeType:   mimeType,
			FileSize:   info.Size,
			TotalLines: 0,
			LinesRead:  0,
			NextOffset: 1,
			EndOfFile:  true,
		}, nil
	}
	if offset > int64(totalLines) {
		return WorkspaceReadFileV2Result{}, xerrors.Errorf("offset %d is beyond the file length of %d lines", offset, totalLines)
	}

	start := int(offset - 1)
	end := totalLines
	if args.Limit > 0 && int64(start)+args.Limit < int64(end) {
		end = start + int(args.Limit)
	}
	content := strings.Join(lines[start:end], "")
	linesRead := end - start
	next := offset + int64(linesRead)
	return WorkspaceReadFileV2Result{
		Path:       args.Path,
		Content:    content,
		Encoding:   "text",
		MimeType:   mimeType,
		FileSize:   info.Size,
		TotalLines: totalLines,
		LinesRead:  linesRead,
		NextOffset: next,
		EndOfFile:  end >= totalLines,
	}, nil
}

var WorkspaceReadFileV2 = Tool[WorkspaceReadFileV2Args, WorkspaceReadFileV2Result]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceReadFileV2,
		Description: `Read a workspace file. Text mode is the default and uses 1-based line offsets while preserving the literal file text. Set binary=true for byte offsets and base64 content. limit is optional; when omitted the complete remaining content is returned.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute file path."},
				"offset":    map[string]any{"type": "integer", "description": "Text: 1-based line number (default 1). Binary: 0-based byte offset (default 0).", "minimum": 0},
				"limit":     map[string]any{"type": "integer", "description": "Optional amount to read. Text: line count. Binary: byte count. If omitted, read the complete remaining content from offset.", "minimum": 1},
				"binary":    map[string]any{"type": "boolean", "description": "Read bytes and return base64 instead of literal text."},
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
					"description": "One or more file read specifications.",
					"minItems":    1,
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
		if len(args.Files) == 0 {
			return WorkspaceReadFilesV2Result{}, xerrors.New("files must contain at least one entry")
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

type WorkspaceWriteFileV2Result struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

var WorkspaceWriteFileV2 = Tool[WorkspaceWriteFileV2Args, WorkspaceWriteFileV2Result]{
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
	MCPAnnotations:     mcpDestructiveIdempotentAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceWriteFileV2Args) (WorkspaceWriteFileV2Result, error) {
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
				return WorkspaceWriteFileV2Result{}, xerrors.Errorf("decode base64 content: %w", err)
			}
			data = decoded
		default:
			return WorkspaceWriteFileV2Result{}, xerrors.New("encoding must be text or base64")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceWriteFileV2Result{}, err
		}
		defer conn.Close()
		if err := conn.WriteFile(ctx, args.Path, bytes.NewReader(data)); err != nil {
			return WorkspaceWriteFileV2Result{}, err
		}
		return WorkspaceWriteFileV2Result{Path: args.Path, BytesWritten: len(data)}, nil
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

type WorkspaceCreateDirectoryResult struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

var WorkspaceCreateDirectory = Tool[WorkspaceCreateDirectoryArgs, WorkspaceCreateDirectoryResult]{
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
	MCPAnnotations:     mcpMutationIdempotentAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceCreateDirectoryArgs) (WorkspaceCreateDirectoryResult, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceCreateDirectoryResult{}, err
		}
		defer conn.Close()

		info, infoErr := conn.FileInfo(ctx, args.Path)
		if infoErr == nil {
			if !info.IsDir {
				return WorkspaceCreateDirectoryResult{}, xerrors.Errorf("path %q already exists and is not a directory", args.Path)
			}
			return WorkspaceCreateDirectoryResult{Path: args.Path, Created: false}, nil
		}
		if !isWorkspaceFileNotFound(infoErr) {
			return WorkspaceCreateDirectoryResult{}, xerrors.Errorf("inspect directory path: %w", infoErr)
		}
		if err := conn.CreateDirectory(ctx, workspacesdk.CreateDirectoryRequest{Path: args.Path, Parents: args.Parents}); err != nil {
			return WorkspaceCreateDirectoryResult{}, err
		}
		return WorkspaceCreateDirectoryResult{Path: args.Path, Created: true}, nil
	},
}

type WorkspaceMoveFileArgs struct {
	Workspace string `json:"workspace"`
	Source    string `json:"source"`
	Dest      string `json:"dest"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type WorkspaceMoveFileResult struct {
	Source string `json:"source"`
	Dest   string `json:"dest"`
}

var WorkspaceMoveFile = Tool[WorkspaceMoveFileArgs, WorkspaceMoveFileResult]{
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
	Handler: func(ctx context.Context, deps Deps, args WorkspaceMoveFileArgs) (WorkspaceMoveFileResult, error) {
		if filepath.Clean(args.Source) == filepath.Clean(args.Dest) {
			return WorkspaceMoveFileResult{Source: args.Source, Dest: args.Dest}, nil
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceMoveFileResult{}, err
		}
		defer conn.Close()
		if err := conn.MoveFile(ctx, workspacesdk.MoveFileRequest{Source: args.Source, Dest: args.Dest, Overwrite: args.Overwrite}); err != nil {
			return WorkspaceMoveFileResult{}, err
		}
		return WorkspaceMoveFileResult{Source: args.Source, Dest: args.Dest}, nil
	},
}
