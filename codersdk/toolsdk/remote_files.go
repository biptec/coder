package toolsdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func toolShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runHelperCommand(ctx context.Context, conn workspacesdk.AgentConn, req workspacesdk.RunCommandRequest) ([]byte, error) {
	resp, err := conn.RunCommand(ctx, req)
	if err != nil {
		return nil, err
	}
	stdout, err := base64.StdEncoding.DecodeString(resp.StdoutBase64)
	if err != nil {
		return nil, xerrors.Errorf("decode helper stdout: %w", err)
	}
	if resp.ExitCode != 0 {
		detail := strings.TrimSpace(resp.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", resp.ExitCode)
		}
		return nil, xerrors.Errorf("command failed: %s", detail)
	}
	return stdout, nil
}

func remoteFileInfo(ctx context.Context, conn workspacesdk.AgentConn, host, identityFile, filePath string) (workspacesdk.WorkspaceFileInfo, error) {
	if !path.IsAbs(filePath) {
		return workspacesdk.WorkspaceFileInfo{}, xerrors.Errorf("path must be absolute: %q", filePath)
	}
	out, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{
		Host:         host,
		IdentityFile: identityFile,
		Command:      "stat -c '%F\\t%s\\t%Y\\t%A' -- " + toolShellQuote(filePath),
	})
	if err != nil {
		return workspacesdk.WorkspaceFileInfo{}, err
	}
	fields := strings.SplitN(strings.TrimSuffix(string(out), "\n"), "\t", 4)
	if len(fields) != 4 {
		return workspacesdk.WorkspaceFileInfo{}, xerrors.New("unexpected remote stat output")
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return workspacesdk.WorkspaceFileInfo{}, xerrors.Errorf("parse remote file size: %w", err)
	}
	modTime, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return workspacesdk.WorkspaceFileInfo{}, xerrors.Errorf("parse remote modification time: %w", err)
	}
	fileType := fields[0]
	return workspacesdk.WorkspaceFileInfo{
		Path:        filePath,
		Name:        path.Base(filePath),
		IsDir:       fileType == "directory",
		IsSymlink:   strings.Contains(fileType, "symbolic link"),
		Size:        size,
		Mode:        fields[3],
		ModTimeUnix: modTime,
	}, nil
}

//nolint:revive // includeHidden is a direct semantic option from list_directory.
func remoteListDirectory(ctx context.Context, conn workspacesdk.AgentConn, host, identityFile, root string, depth int, includeHidden bool, cursor, limit int) (WorkspaceListDirectoryV2Result, error) {
	if !path.IsAbs(root) {
		return WorkspaceListDirectoryV2Result{}, xerrors.Errorf("path must be absolute: %q", root)
	}
	command := fmt.Sprintf("find %s -mindepth 1 -maxdepth %d -printf '%%p\\0%%y\\0%%s\\0%%T@\\0%%M\\0'", toolShellQuote(root), depth)
	out, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{Host: host, IdentityFile: identityFile, Command: command})
	if err != nil {
		return WorkspaceListDirectoryV2Result{}, err
	}
	parts := strings.Split(string(out), "\x00")
	entries := make([]WorkspaceDirectoryEntry, 0, len(parts)/5)
	for i := 0; i+4 < len(parts); i += 5 {
		entryPath := parts[i]
		if entryPath == "" {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(entryPath, root), "/")
		if !includeHidden && remotePathHidden(rel) {
			continue
		}
		size, sizeErr := strconv.ParseInt(parts[i+2], 10, 64)
		if sizeErr != nil {
			continue
		}
		modFloat, modErr := strconv.ParseFloat(parts[i+3], 64)
		if modErr != nil {
			continue
		}
		entries = append(entries, WorkspaceDirectoryEntry{
			Path:        entryPath,
			Name:        path.Base(entryPath),
			IsDir:       parts[i+1] == "d",
			IsSymlink:   parts[i+1] == "l",
			Size:        size,
			Mode:        parts[i+4],
			ModTimeUnix: int64(modFloat),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	if len(entries) > maxDirectoryTraversalEntries {
		entries = entries[:maxDirectoryTraversalEntries]
	}
	if cursor > len(entries) {
		cursor = len(entries)
	}
	end := cursor + limit
	if end > len(entries) {
		end = len(entries)
	}
	page := append([]WorkspaceDirectoryEntry(nil), entries[cursor:end]...)
	var next *int
	if end < len(entries) {
		value := end
		next = &value
	}
	return WorkspaceListDirectoryV2Result{Entries: page, NextCursor: next}, nil
}

func remotePathHidden(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func remoteReadWorkspaceFileV2(ctx context.Context, conn workspacesdk.AgentConn, args WorkspaceReadFileV2Args) (WorkspaceReadFileV2Result, error) {
	info, err := remoteFileInfo(ctx, conn, args.Host, args.IdentityFile, args.Path)
	if err != nil {
		return WorkspaceReadFileV2Result{}, err
	}
	if info.IsDir {
		return WorkspaceReadFileV2Result{}, xerrors.Errorf("path %q is a directory", args.Path)
	}
	if args.Binary {
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
		command := fmt.Sprintf("dd if=%s bs=1 skip=%d count=%d 2>/dev/null", toolShellQuote(args.Path), offset, limit)
		data, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{Host: args.Host, IdentityFile: args.IdentityFile, Command: command})
		if err != nil {
			return WorkspaceReadFileV2Result{}, err
		}
		next := offset + int64(len(data))
		return WorkspaceReadFileV2Result{
			Path:       args.Path,
			Content:    base64.StdEncoding.EncodeToString(data),
			Encoding:   "base64",
			FileSize:   info.Size,
			NextOffset: next,
			EndOfFile:  next >= info.Size,
		}, nil
	}

	limits := workspacesdk.DefaultReadFileLinesLimits()
	if info.Size > limits.MaxFileSize {
		return WorkspaceReadFileV2Result{}, xerrors.Errorf("file is %d bytes which exceeds the maximum of %d bytes", info.Size, limits.MaxFileSize)
	}
	data, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{
		Host: args.Host, IdentityFile: args.IdentityFile, Argv: []string{"cat", "--", args.Path},
	})
	if err != nil {
		return WorkspaceReadFileV2Result{}, err
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
	if limit < 1 || limit > int64(limits.MaxResponseLines) {
		return WorkspaceReadFileV2Result{}, xerrors.Errorf("text limit must be between 1 and %d lines", limits.MaxResponseLines)
	}
	if len(data) == 0 {
		return WorkspaceReadFileV2Result{Path: args.Path, Encoding: "text", FileSize: info.Size, NextOffset: offset, EndOfFile: true}, nil
	}
	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)
	if offset > int64(totalLines) {
		return WorkspaceReadFileV2Result{}, xerrors.Errorf("offset %d is beyond the file length of %d lines", offset, totalLines)
	}
	start := int(offset - 1)
	end := start + int(limit)
	if end > totalLines {
		end = totalLines
	}
	formatted := make([]string, 0, end-start)
	totalBytes := 0
	for i := start; i < end; i++ {
		line := lines[i]
		if len(line) > limits.MaxLineBytes {
			line = line[:limits.MaxLineBytes] + "... [truncated]"
		}
		numbered := fmt.Sprintf("%d\t%s", i+1, line)
		newTotal := totalBytes + len(numbered)
		if len(formatted) > 0 {
			newTotal++
		}
		if newTotal > limits.MaxResponseBytes {
			return WorkspaceReadFileV2Result{}, xerrors.Errorf("output would exceed %d bytes; read less using offset and limit", limits.MaxResponseBytes)
		}
		formatted = append(formatted, numbered)
		totalBytes = newTotal
	}
	next := offset + int64(len(formatted))
	return WorkspaceReadFileV2Result{
		Path:       args.Path,
		Content:    strings.Join(formatted, "\n"),
		Encoding:   "text",
		FileSize:   info.Size,
		TotalLines: totalLines,
		LinesRead:  len(formatted),
		NextOffset: next,
		EndOfFile:  next > int64(totalLines),
	}, nil
}

func remoteWriteFile(ctx context.Context, conn workspacesdk.AgentConn, host, identityFile, filePath string, data []byte) error {
	if !path.IsAbs(filePath) {
		return xerrors.Errorf("path must be absolute: %q", filePath)
	}
	dir := path.Dir(filePath)
	base := path.Base(filePath)
	command := "set -eu; dest=" + toolShellQuote(filePath) + "; dir=" + toolShellQuote(dir) + "; base=" + toolShellQuote(base) + "; " +
		"if [ -L \"$dest\" ]; then resolved=$(readlink -f -- \"$dest\"); dest=\"$resolved\"; dir=$(dirname -- \"$dest\"); base=$(basename -- \"$dest\"); fi; " +
		"mkdir -p -- \"$dir\"; tmp=$(mktemp -- \"$dir/.${base}.coder-write.XXXXXX\"); " +
		"cleanup(){ rm -f -- \"$tmp\"; }; trap cleanup EXIT HUP INT TERM; " +
		"old_mode=''; if [ -e \"$dest\" ] && [ ! -L \"$dest\" ]; then old_mode=$(stat -c '%a' -- \"$dest\" 2>/dev/null || true); fi; " +
		"cat > \"$tmp\"; if [ -n \"$old_mode\" ]; then chmod \"$old_mode\" -- \"$tmp\"; fi; " +
		"mv -f -- \"$tmp\" \"$dest\"; trap - EXIT HUP INT TERM"
	_, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{
		Host:         host,
		IdentityFile: identityFile,
		Command:      command,
		StdinBase64:  base64.StdEncoding.EncodeToString(data),
	})
	return err
}

//nolint:revive // parents mirrors the create_directory tool option.
func remoteCreateDirectory(ctx context.Context, conn workspacesdk.AgentConn, host, identityFile, dirPath string, parents bool) error {
	if !path.IsAbs(dirPath) {
		return xerrors.Errorf("path must be absolute: %q", dirPath)
	}
	flag := ""
	if parents {
		flag = "-p "
	}
	command := "if [ -d " + toolShellQuote(dirPath) + " ]; then exit 0; fi; mkdir " + flag + "-- " + toolShellQuote(dirPath)
	_, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{
		Host: host, IdentityFile: identityFile, Command: command,
	})
	return err
}

//nolint:revive // dryRun mirrors the edit tool option and is intentionally explicit here.
func remoteEditFiles(ctx context.Context, conn workspacesdk.AgentConn, host, identityFile string, files []workspacesdk.FileEdits, dryRun bool) (workspacesdk.FileEditResponse, error) {
	if len(files) == 0 {
		return workspacesdk.FileEditResponse{}, xerrors.New("must specify at least one file")
	}
	type stagedFile struct {
		remote string
		temp   string
		edits  []workspacesdk.FileEdit
	}
	staged := make([]stagedFile, 0, len(files))
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, file := range staged {
			_, _ = runHelperCommand(cleanupCtx, conn, workspacesdk.RunCommandRequest{Argv: []string{"rm", "-f", "--", file.temp}})
		}
	}()

	for _, file := range files {
		data, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{
			Host: host, IdentityFile: identityFile, Argv: []string{"cat", "--", file.Path},
		})
		if err != nil {
			return workspacesdk.FileEditResponse{}, xerrors.Errorf("read remote file %s: %w", file.Path, err)
		}
		temp := "/tmp/coder-mcp-remote-edit-" + uuid.NewString()
		if err := conn.WriteFile(ctx, temp, bytes.NewReader(data)); err != nil {
			return workspacesdk.FileEditResponse{}, xerrors.Errorf("stage remote file %s: %w", file.Path, err)
		}
		staged = append(staged, stagedFile{remote: file.Path, temp: temp, edits: file.Edits})
	}

	requestFiles := make([]workspacesdk.FileEdits, 0, len(staged))
	for _, file := range staged {
		requestFiles = append(requestFiles, workspacesdk.FileEdits{Path: file.temp, Edits: file.edits})
	}
	resp, err := conn.EditFiles(ctx, workspacesdk.FileEditRequest{Files: requestFiles, IncludeDiff: true, DryRun: dryRun})
	if err != nil {
		return workspacesdk.FileEditResponse{}, err
	}
	for i := range resp.Files {
		if i >= len(staged) {
			break
		}
		resp.Files[i].Path = staged[i].remote
		resp.Files[i].Diff = strings.ReplaceAll(resp.Files[i].Diff, staged[i].temp, staged[i].remote)
	}
	if dryRun {
		return resp, nil
	}
	for _, file := range staged {
		data, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{Argv: []string{"cat", "--", file.temp}})
		if err != nil {
			return workspacesdk.FileEditResponse{}, xerrors.Errorf("read staged edit for %s: %w", file.remote, err)
		}
		if err := remoteWriteFile(ctx, conn, host, identityFile, file.remote, data); err != nil {
			return workspacesdk.FileEditResponse{}, xerrors.Errorf("write edited remote file %s: %w", file.remote, err)
		}
	}
	return resp, nil
}

//nolint:revive // overwrite mirrors the move_file tool option.
func remoteMoveFile(ctx context.Context, conn workspacesdk.AgentConn, host, identityFile, source, dest string, overwrite bool) error {
	if !path.IsAbs(source) || !path.IsAbs(dest) {
		return xerrors.New("source and dest must be absolute")
	}
	command := "if [ ! -e " + toolShellQuote(source) + " ] && [ ! -L " + toolShellQuote(source) + " ]; then echo 'source does not exist' >&2; exit 2; fi; "
	if overwrite {
		command += "if [ -e " + toolShellQuote(dest) + " ] || [ -L " + toolShellQuote(dest) + " ]; then if [ -d " + toolShellQuote(dest) + " ] && [ ! -L " + toolShellQuote(dest) + " ]; then rmdir -- " + toolShellQuote(dest) + "; else rm -- " + toolShellQuote(dest) + "; fi; fi; "
	} else {
		command += "if [ -e " + toolShellQuote(dest) + " ] || [ -L " + toolShellQuote(dest) + " ]; then echo 'destination exists' >&2; exit 17; fi; "
	}
	command += "mv -- " + toolShellQuote(source) + " " + toolShellQuote(dest)
	_, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{Host: host, IdentityFile: identityFile, Command: command})
	return err
}
