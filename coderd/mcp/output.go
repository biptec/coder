package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/xeipuuv/gojsonschema"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// withAssistantOutputRendering keeps toolsdk typed and applies the public MCP
// presentation contract at the protocol boundary. Structured results are not
// duplicated as text. Opaque payloads stay in content and metadata stays in
// structuredContent.
func withAssistantOutputRendering(serverTool server.ServerTool, publicName string) server.ServerTool {
	serverTool.Tool.OutputSchema = assistantOutputSchema(publicName)
	originalHandler := serverTool.Handler
	serverTool.Handler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		request = withAssistantInputDefaults(publicName, request)
		result, err := originalHandler(ctx, request)
		if err != nil {
			return nil, err
		}
		if result == nil || result.IsError {
			return result, nil
		}
		rendered, ok, err := renderAssistantToolResult(publicName, request.GetArguments(), result)
		if err != nil {
			return nil, xerrors.Errorf("render %s output: %w", publicName, err)
		}
		if !ok {
			return result, nil
		}
		structured, ok := rendered.StructuredContent.(map[string]any)
		if !ok {
			return nil, xerrors.Errorf("render %s output: structuredContent is not an object", publicName)
		}
		if err := validateAssistantStructuredContent(publicName, serverTool.Tool.OutputSchema, structured); err != nil {
			return nil, xerrors.Errorf("validate %s output: %w", publicName, err)
		}
		return rendered, nil
	}
	return serverTool
}

func withAssistantInputDefaults(publicName string, request mcp.CallToolRequest) mcp.CallToolRequest {
	arguments := request.GetArguments()
	var cloned map[string]any
	clone := func() map[string]any {
		if cloned != nil {
			return cloned
		}
		cloned = make(map[string]any, len(arguments)+1)
		for key, value := range arguments {
			cloned[key] = value
		}
		return cloned
	}

	switch publicName {
	case "read_process_output":
		if _, exists := arguments["cursor"]; !exists {
			clone()["cursor"] = float64(0)
		}
	case "get_workspace":
		if workspace, exists := arguments["workspace"]; exists {
			values := clone()
			values["workspace_id"] = workspace
			delete(values, "workspace")
		}
	}
	if cloned != nil {
		request.Params.Arguments = cloned
	}
	return request
}

func renderAssistantToolResult(publicName string, args map[string]any, result *mcp.CallToolResult) (*mcp.CallToolResult, bool, error) {
	raw, ok := assistantResultJSON(result)
	if !ok {
		return result, false, nil
	}

	content, structured, handled, err := renderAssistantJSON(publicName, args, raw)
	if err != nil || !handled {
		return result, handled, err
	}
	result.Content = content
	result.StructuredContent = structured
	return result, true, nil
}

func assistantResultJSON(result *mcp.CallToolResult) ([]byte, bool) {
	if result == nil || len(result.Content) != 1 {
		return nil, false
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return nil, false
	}
	raw := strings.TrimSpace(text.Text)
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil, false
	}
	return []byte(raw), true
}

func renderAssistantJSON(publicName string, args map[string]any, raw []byte) ([]mcp.Content, map[string]any, bool, error) {
	switch publicName {
	case "list_workspaces":
		var value []toolsdk.AccessibleWorkspace
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		sort.Slice(value, func(i, j int) bool { return value[i].FullName < value[j].FullName })
		workspaces := make([]any, 0, len(value))
		for _, workspace := range value {
			workspaces = append(workspaces, map[string]any{
				"workspace": workspace.FullName,
				"status":    workspace.Status,
			})
		}
		return emptyContent(), map[string]any{"workspaces": workspaces}, true, nil

	case "get_workspace":
		var value codersdk.Workspace
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		agents := make([]any, 0)
		for _, resource := range value.LatestBuild.Resources {
			for _, agent := range resource.Agents {
				item := map[string]any{
					"name":    agent.Name,
					"status":  string(agent.Status),
					"version": agent.Version,
				}
				if !agent.LastConnectedAt.IsZero() {
					item["last_connected_at"] = agent.LastConnectedAt.UTC().Format(time.RFC3339)
				}
				agents = append(agents, item)
			}
		}
		return emptyContent(), map[string]any{
			"workspace":    value.OwnerName + "/" + value.Name,
			"status":       string(value.LatestBuild.Status),
			"build_number": value.LatestBuild.BuildNumber,
			"template":     value.TemplateName,
			"agents":       agents,
		}, true, nil

	case "list_apps":
		var value toolsdk.WorkspaceListAppsResponse
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		apps := make([]any, 0, len(value.Apps))
		for _, app := range value.Apps {
			apps = append(apps, map[string]any{"name": app.Name, "url": app.URL})
		}
		return emptyContent(), map[string]any{"apps": apps}, true, nil

	case "get_workspace_capabilities":
		var value toolsdk.WorkspaceCapabilitiesResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		structured, err := renderCapabilitiesStructured(value)
		return emptyContent(), structured, true, err

	case "list_directory":
		var value toolsdk.WorkspaceListDirectoryV2Result
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		root := path.Clean(argumentString(args, "path"))
		entries := make([]any, 0, len(value.Entries))
		for _, entry := range value.Entries {
			entryPath := entry.Path
			if root != "." && root != "/" {
				if rel, ok := strings.CutPrefix(entry.Path, strings.TrimSuffix(root, "/")+"/"); ok {
					entryPath = rel
				}
			} else if root == "/" {
				entryPath = strings.TrimPrefix(entry.Path, "/")
			}
			kind := "file"
			if entry.IsDir {
				kind = "directory"
			} else if entry.IsSymlink {
				kind = "symlink"
			}
			item := map[string]any{"path": entryPath, "type": kind}
			if !entry.IsDir {
				item["size"] = entry.Size
			}
			if entry.Mode != "" {
				item["mode"] = entry.Mode
			}
			if entry.ModTimeUnix > 0 {
				item["modified_at"] = time.Unix(entry.ModTimeUnix, 0).UTC().Format(time.RFC3339)
			}
			entries = append(entries, item)
		}
		structured := map[string]any{"entries": entries}
		if value.NextCursor != "" {
			structured["next_cursor"] = value.NextCursor
		}
		return emptyContent(), structured, true, nil

	case "read_file":
		var value toolsdk.WorkspaceReadFileV2Result
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		if value.Error != "" {
			return []mcp.Content{mcp.NewTextContent(value.Error)}, nil, true, nil
		}
		content := payloadContent(value.Content)
		return content, readFileMetadata(value, args), true, nil

	case "read_multiple_files":
		var value toolsdk.WorkspaceReadFilesV2Result
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		content := make([]mcp.Content, 0, len(value.Files))
		files := make([]any, 0, len(value.Files))
		requestedFiles, _ := args["files"].([]any)
		for fileIndex, file := range value.Files {
			if file.Error != "" {
				files = append(files, map[string]any{
					"path": file.Path,
					"error": map[string]any{
						"code":    fileErrorCode(file.Error),
						"message": file.Error,
					},
				})
				continue
			}
			index := len(content)
			content = append(content, mcp.NewTextContent(file.Content))
			var requested map[string]any
			if fileIndex < len(requestedFiles) {
				requested, _ = requestedFiles[fileIndex].(map[string]any)
			}
			meta := readFileMetadata(file, requested)
			meta["content_index"] = index
			files = append(files, meta)
		}
		return content, map[string]any{"files": files}, true, nil

	case "write_file":
		var value toolsdk.WorkspaceWriteFileV2Result
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		return emptyContent(), map[string]any{
			"path":          value.Path,
			"bytes_written": value.BytesWritten,
		}, true, nil

	case "get_file_info":
		var value workspacesdk.WorkspaceFileInfo
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		kind := "file"
		if value.IsDir {
			kind = "directory"
		} else if value.IsSymlink {
			kind = "symlink"
		}
		structured := map[string]any{
			"path": value.Path,
			"type": kind,
			"size": value.Size,
		}
		if value.Mode != "" {
			structured["mode"] = value.Mode
		}
		if value.ModTimeUnix > 0 {
			structured["modified_at"] = time.Unix(value.ModTimeUnix, 0).UTC().Format(time.RFC3339)
		}
		return emptyContent(), structured, true, nil

	case "create_directory":
		var value toolsdk.WorkspaceCreateDirectoryResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		return emptyContent(), map[string]any{"path": value.Path, "created": value.Created}, true, nil

	case "move_file":
		var value toolsdk.WorkspaceMoveFileResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		return emptyContent(), map[string]any{"source": value.Source, "dest": value.Dest}, true, nil

	case "edit_file", "edit_multiple_files":
		var value toolsdk.WorkspaceEditFilesResponse
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		content := make([]mcp.Content, 0, 1)
		diffs := make([]string, 0, len(value.Files))
		files := make([]any, 0, len(value.Files))
		for _, file := range value.Files {
			replacements := 0
			for _, edit := range file.Edits {
				replacements += edit.ReplacementCount
			}
			files = append(files, map[string]any{
				"path":         file.Path,
				"replacements": replacements,
			})
			if file.Diff != "" {
				diffs = append(diffs, strings.TrimRight(file.Diff, "\n"))
			}
		}
		if len(diffs) > 0 {
			content = append(content, mcp.NewTextContent(strings.Join(diffs, "\n\n")+"\n"))
		}
		structured := map[string]any{"files": files, "files_edited": len(value.Files)}
		if publicName == "edit_file" && len(value.Files) == 1 {
			singleFile, ok := files[0].(map[string]any)
			if !ok {
				return nil, nil, true, xerrors.New("edit_file renderer produced invalid file metadata")
			}
			structured = singleFile
		}
		return content, structured, true, nil

	case "start_search", "get_search_results":
		var value workspacesdk.SearchResultsResponse
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		return emptyContent(), searchResultsStructured(value), true, nil

	case "list_searches":
		var value workspacesdk.ListSearchesResponse
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		searches := make([]any, 0, len(value.Searches))
		for _, search := range value.Searches {
			searches = append(searches, searchSessionStructured(search))
		}
		return emptyContent(), map[string]any{"searches": searches}, true, nil

	case "stop_search":
		return emptyContent(), map[string]any{
			"search_id": argumentString(args, "search_id"),
			"stopped":   true,
		}, true, nil

	case "start_process", "execute_shell_command", "read_process_output":
		var value toolsdk.WorkspaceProcessResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		return payloadContent(value.Output), processStructured(value), true, nil

	case "interact_with_process":
		var value toolsdk.WorkspaceProcessInputResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		structured := map[string]any{
			"process_id":     argumentString(args, "process_id"),
			"input_accepted": value.Success,
		}
		content := emptyContent()
		if value.Process != nil {
			for key, item := range processStructured(*value.Process) {
				if key != "process_id" {
					structured[key] = item
				}
			}
			content = payloadContent(value.Process.Output)
		}
		if value.OutputError != "" {
			structured["output_error"] = value.OutputError
		}
		return content, structured, true, nil

	case "signal_process":
		var value toolsdk.WorkspaceProcessSignalResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		structured := map[string]any{
			"process_id": argumentString(args, "process_id"),
			"signal":     argumentString(args, "signal"),
			"sent":       value.Success,
		}
		if strings.Contains(strings.ToLower(value.Message), "already completed") {
			structured["sent"] = false
			structured["already_completed"] = true
		}
		return emptyContent(), structured, true, nil

	case "list_sessions":
		var value toolsdk.WorkspaceProcessListResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		now := time.Now().Unix()
		sessions := make([]any, 0, len(value.Processes))
		for _, process := range value.Processes {
			status := "completed"
			if process.Running {
				status = "running"
			}
			end := now
			if process.ExitedAt != nil {
				end = *process.ExitedAt
			}
			runtimeMs := int64(0)
			if process.StartedAt > 0 && end >= process.StartedAt {
				runtimeMs = (end - process.StartedAt) * 1000
			}
			command := process.Command
			if command == "" && len(process.Argv) > 0 {
				command = strings.Join(process.Argv, " ")
			}
			item := map[string]any{
				"process_id": process.ID,
				"status":     status,
				"runtime_ms": runtimeMs,
				"command":    command,
				"started_at": time.Unix(process.StartedAt, 0).UTC().Format(time.RFC3339),
			}
			if process.ExitCode != nil {
				item["exit_code"] = *process.ExitCode
			}
			if process.Host != "" {
				target := process.Host
				if process.Port != 0 {
					target += ":" + strconv.Itoa(process.Port)
				}
				item["target"] = target
			}
			sessions = append(sessions, item)
		}
		structured := map[string]any{"sessions": sessions}
		if value.NextCursor != "" {
			structured["next_cursor"] = value.NextCursor
		}
		return emptyContent(), structured, true, nil

	case "list_processes":
		var value toolsdk.WorkspaceListSystemProcessesResult
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, nil, true, err
		}
		processes := make([]any, 0, len(value.Processes))
		for _, process := range value.Processes {
			item := map[string]any{
				"pid":             process.PID,
				"ppid":            process.PPID,
				"user":            process.Username,
				"cpu_percent":     process.CPUPercent,
				"memory_percent":  process.MemoryPercent,
				"elapsed_seconds": process.ElapsedSeconds,
				"command":         process.Command,
			}
			if process.StartedAtUnix > 0 {
				item["started_at"] = time.Unix(process.StartedAtUnix, 0).UTC().Format(time.RFC3339)
			}
			processes = append(processes, item)
		}
		structured := map[string]any{"processes": processes}
		if value.NextCursor != "" {
			structured["next_cursor"] = value.NextCursor
		}
		return emptyContent(), structured, true, nil
	}
	return nil, nil, false, nil
}

func emptyContent() []mcp.Content {
	return []mcp.Content{}
}

func payloadContent(payload string) []mcp.Content {
	if payload == "" {
		return emptyContent()
	}
	return []mcp.Content{mcp.NewTextContent(payload)}
}

func readFileMetadata(value toolsdk.WorkspaceReadFileV2Result, args map[string]any) map[string]any {
	if value.Encoding == "base64" {
		dataLen := int64(0)
		if decoded, err := base64.StdEncoding.DecodeString(value.Content); err == nil {
			dataLen = int64(len(decoded))
		}
		start := value.NextOffset - dataLen
		if start < 0 || (value.NextOffset == 0 && dataLen == 0) {
			start = argumentInt64(args, "offset")
		}
		meta := map[string]any{
			"path":             value.Path,
			"content_encoding": "base64",
			"start_byte":       start,
			"size":             value.FileSize,
			"eof":              value.EndOfFile,
		}
		if dataLen > 0 {
			meta["end_byte"] = start + dataLen - 1
		}
		if value.MimeType != "" {
			meta["mime_type"] = value.MimeType
		}
		if !value.EndOfFile {
			meta["next_offset"] = value.NextOffset
		}
		return meta
	}

	start := value.NextOffset - int64(value.LinesRead)
	if start < 1 {
		start = 1
	}
	meta := map[string]any{
		"path":        value.Path,
		"start_line":  start,
		"total_lines": value.TotalLines,
		"eof":         value.EndOfFile,
	}
	if value.LinesRead > 0 {
		meta["end_line"] = start + int64(value.LinesRead) - 1
	}
	if !value.EndOfFile {
		meta["next_offset"] = value.NextOffset
	}
	return meta
}

func fileErrorCode(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "not found") || strings.Contains(lower, "no such file") {
		return "not_found"
	}
	if strings.Contains(lower, "permission") || strings.Contains(lower, "denied") {
		return "permission_denied"
	}
	return "read_failed"
}

func searchStatus(status string) string {
	switch status {
	case "complete":
		return "completed"
	default:
		return status
	}
}

func searchSessionStructured(search workspacesdk.SearchSessionInfo) map[string]any {
	item := map[string]any{
		"search_id": search.ID,
		"status":    searchStatus(search.Status),
		"mode":      search.Mode,
		"query":     search.Query,
		"results":   search.ResultCount,
	}
	if search.CreatedAt > 0 {
		item["created_at"] = time.Unix(search.CreatedAt, 0).UTC().Format(time.RFC3339)
	}
	if search.CompletedAt != nil {
		item["completed_at"] = time.Unix(*search.CompletedAt, 0).UTC().Format(time.RFC3339)
	}
	if search.Truncated {
		item["truncated"] = true
	}
	if search.Error != "" {
		item["error"] = search.Error
	}
	return item
}

func searchResultsStructured(value workspacesdk.SearchResultsResponse) map[string]any {
	results := make([]any, 0, len(value.Results))
	for _, result := range value.Results {
		item := map[string]any{"path": result.Path}
		if result.Line > 0 {
			item["line"] = result.Line
		}
		if result.Column > 0 {
			item["column"] = result.Column
		}
		if result.Text != "" {
			item["text"] = result.Text
		}
		results = append(results, item)
	}
	structured := map[string]any{
		"search_id":         value.Search.ID,
		"status":            searchStatus(value.Search.Status),
		"results_available": value.Search.ResultCount,
		"results":           results,
	}
	if value.Search.Error != "" {
		structured["error"] = value.Search.Error
	}
	if value.Search.Truncated {
		structured["truncated"] = true
	}
	if value.NextCursor != nil {
		structured["next_cursor"] = *value.NextCursor
	}
	return structured
}

func processStructured(value toolsdk.WorkspaceProcessResult) map[string]any {
	status := "completed"
	if value.Running {
		status = "running"
	}
	cursor := int64(0)
	if value.NextCursor != nil {
		cursor = *value.NextCursor
	}
	structured := map[string]any{
		"process_id":    value.ProcessID,
		"status":        status,
		"output_cursor": cursor,
	}
	if !value.Running && value.ExitCode != nil {
		structured["exit_code"] = *value.ExitCode
	}
	if value.GapBytes > 0 {
		structured["output_gap_bytes"] = value.GapBytes
	}
	if value.HasMore {
		structured["has_more"] = true
	}
	if value.Truncated != nil && value.Truncated.OmittedBytes > 0 {
		structured["truncated_bytes"] = value.Truncated.OmittedBytes
	}
	return structured
}

func renderCapabilitiesStructured(value toolsdk.WorkspaceCapabilitiesResult) (map[string]any, error) {
	if !value.Available {
		result := map[string]any{"available": false}
		if value.Message != "" {
			result["message"] = value.Message
		}
		return result, nil
	}

	var manifest map[string]any
	if err := json.Unmarshal(value.Manifest, &manifest); err != nil {
		return nil, err
	}
	groups := make([]any, 0, 4)
	addGroup := func(name string, items []string) {
		if len(items) == 0 {
			return
		}
		groups = append(groups, map[string]any{"name": name, "items": items})
	}

	systemItems := make([]string, 0, 2)
	if system, ok := manifest["system"].(map[string]any); ok {
		for _, key := range []string{"os", "architecture"} {
			if v := stringValue(system[key]); v != "" {
				systemItems = append(systemItems, key+": "+v)
			}
		}
	}
	addGroup("System", systemItems)

	versioned := make([]string, 0)
	if items, ok := manifest["versioned_tools"].([]any); ok {
		for _, rawTool := range items {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			name := stringValue(tool["name"])
			if name == "" {
				continue
			}
			if version := stringValue(tool["version"]); version != "" {
				name += " " + version
			}
			versioned = append(versioned, name)
		}
	}
	sort.Strings(versioned)
	addGroup("Versioned tools", versioned)

	browsers := stringSlice(manifest["browser_artifacts"])
	sort.Strings(browsers)
	addGroup("Browser artifacts", browsers)

	meta := make([]string, 0, 4)
	for _, key := range []string{"build_id", "profile"} {
		if v := stringValue(manifest[key]); v != "" {
			meta = append(meta, key+": "+v)
		}
	}
	if items, ok := manifest["apt_packages"].([]any); ok {
		meta = append(meta, fmt.Sprintf("apt_packages: %d", len(items)))
	}
	if items, ok := manifest["published_commands"].([]any); ok {
		meta = append(meta, fmt.Sprintf("published_commands: %d", len(items)))
	}
	addGroup("Image metadata", meta)

	return map[string]any{"available": true, "groups": groups}, nil
}

func stringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if value := stringValue(item); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func argumentString(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func argumentInt64(args map[string]any, key string) int64 {
	if args == nil {
		return 0
	}
	switch value := args[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	default:
		return 0
	}
}

func stringValue(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func assistantOutputSchema(publicName string) mcp.ToolOutputSchema {
	stringProp := func() map[string]any { return map[string]any{"type": "string"} }
	intProp := func() map[string]any { return map[string]any{"type": "integer"} }
	numberProp := func() map[string]any { return map[string]any{"type": "number"} }
	boolProp := func() map[string]any { return map[string]any{"type": "boolean"} }
	object := func(required []string, properties map[string]any) map[string]any {
		out := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	array := func(items map[string]any) map[string]any { return map[string]any{"type": "array", "items": items} }
	top := func(required []string, properties map[string]any) mcp.ToolOutputSchema {
		return mcp.ToolOutputSchema{Type: "object", Properties: properties, Required: required}
	}

	switch publicName {
	case "list_recent_tool_calls":
		call := object([]string{"started_at", "status", "tool"}, map[string]any{
			"started_at": stringProp(), "status": stringProp(), "tool": stringProp(),
			"process_id": stringProp(), "search_id": stringProp(),
		})
		return top([]string{"calls"}, map[string]any{"calls": array(call), "next_cursor": stringProp()})
	case "list_workspaces":
		item := object([]string{"workspace", "status"}, map[string]any{"workspace": stringProp(), "status": stringProp()})
		return top([]string{"workspaces"}, map[string]any{"workspaces": array(item)})
	case "get_workspace":
		agent := object([]string{"name", "status", "version"}, map[string]any{
			"name": stringProp(), "status": stringProp(), "version": stringProp(), "last_connected_at": stringProp(),
		})
		return top([]string{"workspace", "status", "build_number", "template", "agents"}, map[string]any{
			"workspace": stringProp(), "status": stringProp(), "build_number": intProp(), "template": stringProp(), "agents": array(agent),
		})
	case "list_apps":
		item := object([]string{"name", "url"}, map[string]any{"name": stringProp(), "url": stringProp()})
		return top([]string{"apps"}, map[string]any{"apps": array(item)})
	case "get_workspace_capabilities":
		group := object([]string{"name", "items"}, map[string]any{"name": stringProp(), "items": array(stringProp())})
		return top([]string{"available"}, map[string]any{"available": boolProp(), "groups": array(group), "message": stringProp()})
	case "list_directory":
		item := object([]string{"path", "type"}, map[string]any{
			"path": stringProp(), "type": stringProp(), "size": intProp(), "mode": stringProp(), "modified_at": stringProp(),
		})
		return top([]string{"entries"}, map[string]any{"entries": array(item), "next_cursor": stringProp()})
	case "read_file":
		return top([]string{"path", "eof"}, map[string]any{
			"path": stringProp(), "start_line": intProp(), "end_line": intProp(), "total_lines": intProp(),
			"start_byte": intProp(), "end_byte": intProp(), "size": intProp(), "next_offset": intProp(),
			"eof": boolProp(), "content_encoding": stringProp(), "mime_type": stringProp(),
		})
	case "read_multiple_files":
		errObj := object([]string{"code", "message"}, map[string]any{"code": stringProp(), "message": stringProp()})
		item := object([]string{"path"}, map[string]any{
			"path": stringProp(), "content_index": intProp(), "start_line": intProp(), "end_line": intProp(),
			"total_lines": intProp(), "start_byte": intProp(), "end_byte": intProp(), "size": intProp(),
			"next_offset": intProp(), "eof": boolProp(), "content_encoding": stringProp(), "mime_type": stringProp(), "error": errObj,
		})
		return top([]string{"files"}, map[string]any{"files": array(item)})
	case "write_file":
		return top([]string{"path", "bytes_written"}, map[string]any{"path": stringProp(), "bytes_written": intProp()})
	case "get_file_info":
		return top([]string{"path", "type", "size"}, map[string]any{
			"path": stringProp(), "type": stringProp(), "size": intProp(), "mode": stringProp(), "modified_at": stringProp(),
		})
	case "create_directory":
		return top([]string{"path", "created"}, map[string]any{"path": stringProp(), "created": boolProp()})
	case "move_file":
		return top([]string{"source", "dest"}, map[string]any{"source": stringProp(), "dest": stringProp()})
	case "edit_file":
		return top([]string{"path", "replacements"}, map[string]any{"path": stringProp(), "replacements": intProp()})
	case "edit_multiple_files":
		item := object([]string{"path", "replacements"}, map[string]any{"path": stringProp(), "replacements": intProp()})
		return top([]string{"files", "files_edited"}, map[string]any{"files": array(item), "files_edited": intProp()})
	case "start_search", "get_search_results":
		item := object([]string{"path"}, map[string]any{"path": stringProp(), "line": intProp(), "column": intProp(), "text": stringProp()})
		return top([]string{"search_id", "status", "results_available", "results"}, map[string]any{
			"search_id": stringProp(), "status": stringProp(), "results_available": intProp(), "results": array(item),
			"next_cursor": intProp(), "error": stringProp(), "truncated": boolProp(),
		})
	case "list_searches":
		item := object([]string{"search_id", "status", "mode", "query", "results"}, map[string]any{
			"search_id": stringProp(), "status": stringProp(), "mode": stringProp(), "query": stringProp(),
			"results": intProp(), "created_at": stringProp(), "completed_at": stringProp(), "error": stringProp(), "truncated": boolProp(),
		})
		return top([]string{"searches"}, map[string]any{"searches": array(item)})
	case "stop_search":
		return top([]string{"search_id", "stopped"}, map[string]any{"search_id": stringProp(), "stopped": boolProp()})
	case "start_process", "execute_shell_command", "read_process_output":
		return processOutputSchema(top, stringProp, intProp, boolProp)
	case "interact_with_process":
		return top([]string{"process_id", "input_accepted"}, map[string]any{
			"process_id": stringProp(), "input_accepted": boolProp(), "status": stringProp(), "exit_code": intProp(),
			"output_cursor": intProp(), "output_gap_bytes": intProp(), "has_more": boolProp(), "truncated_bytes": intProp(), "output_error": stringProp(),
		})
	case "signal_process":
		return top([]string{"process_id", "signal", "sent"}, map[string]any{
			"process_id": stringProp(), "signal": stringProp(), "sent": boolProp(), "already_completed": boolProp(),
		})
	case "list_sessions":
		item := object([]string{"process_id", "status", "runtime_ms", "command", "started_at"}, map[string]any{
			"process_id": stringProp(), "status": stringProp(), "runtime_ms": intProp(), "command": stringProp(),
			"started_at": stringProp(), "exit_code": intProp(), "target": stringProp(),
		})
		return top([]string{"sessions"}, map[string]any{"sessions": array(item), "next_cursor": stringProp()})
	case "list_processes":
		item := object([]string{"pid", "ppid", "user", "cpu_percent", "memory_percent", "elapsed_seconds", "command"}, map[string]any{
			"pid": intProp(), "ppid": intProp(), "user": stringProp(), "cpu_percent": numberProp(),
			"memory_percent": numberProp(), "elapsed_seconds": intProp(), "command": stringProp(), "started_at": stringProp(),
		})
		return top([]string{"processes"}, map[string]any{"processes": array(item), "next_cursor": stringProp()})
	default:
		return mcp.ToolOutputSchema{}
	}
}

func processOutputSchema(
	top func([]string, map[string]any) mcp.ToolOutputSchema,
	stringProp func() map[string]any,
	intProp func() map[string]any,
	boolProp func() map[string]any,
) mcp.ToolOutputSchema {
	return top([]string{"process_id", "status", "output_cursor"}, map[string]any{
		"process_id": stringProp(), "status": stringProp(), "exit_code": intProp(), "output_cursor": intProp(),
		"output_gap_bytes": intProp(), "has_more": boolProp(), "truncated_bytes": intProp(),
	})
}

var assistantSchemaValidators sync.Map

func validateAssistantStructuredContent(publicName string, schema mcp.ToolOutputSchema, structured map[string]any) error {
	if schema.Type == "" {
		return nil
	}
	if structured == nil {
		return xerrors.New("structuredContent is required by outputSchema")
	}

	var validator *gojsonschema.Schema
	if cached, ok := assistantSchemaValidators.Load(publicName); ok {
		cachedValidator, valid := cached.(*gojsonschema.Schema)
		if !valid {
			return xerrors.New("cached output schema validator has unexpected type")
		}
		validator = cachedValidator
	} else {
		raw, err := json.Marshal(schema)
		if err != nil {
			return xerrors.Errorf("marshal output schema: %w", err)
		}
		compiled, err := gojsonschema.NewSchema(gojsonschema.NewBytesLoader(raw))
		if err != nil {
			return xerrors.Errorf("compile output schema: %w", err)
		}
		actual, _ := assistantSchemaValidators.LoadOrStore(publicName, compiled)
		actualValidator, valid := actual.(*gojsonschema.Schema)
		if !valid {
			return xerrors.New("output schema validator cache stored unexpected type")
		}
		validator = actualValidator
	}

	validation, err := validator.Validate(gojsonschema.NewGoLoader(structured))
	if err != nil {
		return xerrors.Errorf("validate structured content: %w", err)
	}
	if validation.Valid() {
		return nil
	}
	messages := make([]string, 0, len(validation.Errors()))
	for _, item := range validation.Errors() {
		messages = append(messages, item.String())
	}
	return xerrors.Errorf("structuredContent does not match outputSchema: %s", strings.Join(messages, "; "))
}
