package toolsdk

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

type countingReadCloser struct {
	reader    *strings.Reader
	bytesRead int
}

func newCountingReadCloser(content string) *countingReadCloser {
	return &countingReadCloser{reader: strings.NewReader(content)}
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytesRead += n
	return n, err
}

func (*countingReadCloser) Close() error { return nil }

func TestReadWorkspaceFileV2TextPositiveLimitDoesNotReadWholeLargeFile(t *testing.T) {
	t.Parallel()

	content := "first\n" + strings.Repeat("later line\n", 300_000)
	reader := newCountingReadCloser(content)
	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/large").Return("/large", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/large").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/large",
		Size: int64(len(content)),
	}, nil)
	conn.EXPECT().ReadFile(gomock.Any(), "/large", int64(0), int64(0)).Return(reader, "text/plain", nil)

	result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
		Path:  "/large",
		Limit: 1,
	}, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "first\n", result.Content)
	require.Equal(t, 1, result.LinesRead)
	require.False(t, result.EndOfFile)
	require.Zero(t, result.TotalLines, "bounded read must not scan to EOF just to count total lines")
	require.Less(t, reader.bytesRead, len(content), "positive line limit must bound actual I/O")
	require.LessOrEqual(t, reader.bytesRead, 128<<10, "a one-line read should not pull megabytes through the stream")
}

func TestReadWorkspaceFileV2UnlimitedOversizeStopsAtSafetyBudget(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("x", 2<<20)
	reader := newCountingReadCloser(content)
	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/huge-line").Return("/huge-line", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/huge-line").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/huge-line",
		Size: int64(len(content)),
	}, nil)
	conn.EXPECT().ReadFile(gomock.Any(), "/huge-line", int64(0), int64(0)).Return(reader, "text/plain", nil)

	_, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
		Path:  "/huge-line",
		Limit: 0,
	}, 1024)
	require.Error(t, err)
	require.ErrorContains(t, err, "MCP response safety budget")
	require.ErrorContains(t, err, "smaller positive limit")
	require.Less(t, reader.bytesRead, len(content), "budget overflow must stop streaming before reading the whole file")
}

func TestReadWorkspaceFileV2BinaryRejectsOversizeBeforeRead(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/large.bin").Return("/large.bin", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/large.bin").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/large.bin",
		Size: 900,
	}, nil)

	_, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
		Path:   "/large.bin",
		Binary: true,
		Limit:  0,
	}, 1000)
	require.Error(t, err)
	require.ErrorContains(t, err, "base64 bytes")
	require.ErrorContains(t, err, "MCP response safety budget")
}

func TestReadWorkspaceFileV2TextHandlesNoFinalNewlineAndLargeSingleLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
		lines   int
	}{
		{name: "no-final-newline", content: "alpha\nbeta", lines: 2},
		{name: "large-single-line", content: strings.Repeat("z", 100_000), lines: 1},
		{name: "empty", content: "", lines: 0},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			conn := agentconnmock.NewMockAgentConn(ctrl)
			conn.EXPECT().ResolvePath(gomock.Any(), "/file").Return("/file", nil)
			conn.EXPECT().FileInfo(gomock.Any(), "/file").Return(workspacesdk.WorkspaceFileInfo{
				Path: "/file",
				Size: int64(len(tc.content)),
			}, nil)
			conn.EXPECT().ReadFile(gomock.Any(), "/file", int64(0), int64(0)).Return(
				io.NopCloser(strings.NewReader(tc.content)), "text/plain", nil,
			)

			result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
				Path:  "/file",
				Limit: 0,
			}, 256<<10)
			require.NoError(t, err)
			require.Equal(t, tc.content, result.Content)
			require.Equal(t, tc.lines, result.LinesRead)
			require.True(t, result.EndOfFile)
		})
	}
}

func TestReadWorkspaceFilesV2UsesCombinedPayloadBudget(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	gomock.InOrder(
		conn.EXPECT().ResolvePath(gomock.Any(), "/first").Return("/first", nil),
		conn.EXPECT().FileInfo(gomock.Any(), "/first").Return(workspacesdk.WorkspaceFileInfo{Path: "/first", Size: 4}, nil),
		conn.EXPECT().ReadFile(gomock.Any(), "/first", int64(0), int64(0)).Return(io.NopCloser(strings.NewReader("abc\n")), "text/plain", nil),
		conn.EXPECT().ResolvePath(gomock.Any(), "/second").Return("/second", nil),
		conn.EXPECT().FileInfo(gomock.Any(), "/second").Return(workspacesdk.WorkspaceFileInfo{Path: "/second", Size: 4}, nil),
		conn.EXPECT().ReadFile(gomock.Any(), "/second", int64(0), int64(0)).Return(io.NopCloser(strings.NewReader("def\n")), "text/plain", nil),
	)

	result := readWorkspaceFilesV2(context.Background(), conn, []WorkspaceReadFileV2Args{
		{Path: "/first", Limit: 0},
		{Path: "/second", Limit: 0},
	}, 6)
	require.Len(t, result.Files, 2)
	require.Equal(t, "abc\n", result.Files[0].Content)
	require.Empty(t, result.Files[0].Error)
	require.Empty(t, result.Files[1].Content)
	require.Contains(t, result.Files[1].Error, "MCP response safety budget")
}

func TestReadWorkspaceFileV2BinaryUsesResolvedTargetSize(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/link").Return("/target", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/target").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/target",
		Size: 6,
	}, nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/link").Return(workspacesdk.WorkspaceFileInfo{
		Path:      "/link",
		IsSymlink: true,
	}, nil)
	conn.EXPECT().ReadFile(gomock.Any(), "/link", int64(0), int64(4)).Return(
		io.NopCloser(strings.NewReader("abcd")), "application/octet-stream", nil,
	)

	result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
		Path:   "/link",
		Binary: true,
		Limit:  4,
	}, 1<<20)
	require.NoError(t, err)
	require.Equal(t, int64(6), result.FileSize)
	require.True(t, result.IsSymlink)
	require.Equal(t, "/target", result.ResolvedPath)
	require.Equal(t, int64(4), result.NextOffset)
	require.False(t, result.EndOfFile, "EOF must be based on the resolved target size, not symlink metadata")
}

func TestReadWorkspaceFilesV2KeepsPerFileErrorsPartial(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	gomock.InOrder(
		conn.EXPECT().ResolvePath(gomock.Any(), "/missing").Return("", os.ErrNotExist),
		conn.EXPECT().ResolvePath(gomock.Any(), "/ok").Return("/ok", nil),
		conn.EXPECT().FileInfo(gomock.Any(), "/ok").Return(workspacesdk.WorkspaceFileInfo{Path: "/ok", Size: 3}, nil),
		conn.EXPECT().ReadFile(gomock.Any(), "/ok", int64(0), int64(0)).Return(io.NopCloser(strings.NewReader("ok\n")), "text/plain", nil),
	)

	result := readWorkspaceFilesV2(context.Background(), conn, []WorkspaceReadFileV2Args{
		{Path: "/missing", Limit: 0},
		{Path: "/ok", Limit: 0},
	}, 1<<20)
	require.Len(t, result.Files, 2)
	require.Equal(t, "/missing", result.Files[0].Path)
	require.Contains(t, result.Files[0].Error, "resolve file path")
	require.Empty(t, result.Files[0].Content)
	require.Equal(t, "ok\n", result.Files[1].Content)
	require.Empty(t, result.Files[1].Error)
}

func TestReadWorkspaceFilesV2MixedLimitsAndSpecialFileError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	gomock.InOrder(
		conn.EXPECT().ResolvePath(gomock.Any(), "/bounded").Return("/bounded", nil),
		conn.EXPECT().FileInfo(gomock.Any(), "/bounded").Return(workspacesdk.WorkspaceFileInfo{Path: "/bounded", Size: 8}, nil),
		conn.EXPECT().ReadFile(gomock.Any(), "/bounded", int64(0), int64(0)).Return(
			io.NopCloser(strings.NewReader("one\ntwo\n")), "text/plain", nil,
		),
		conn.EXPECT().ResolvePath(gomock.Any(), "/unlimited").Return("/unlimited", nil),
		conn.EXPECT().FileInfo(gomock.Any(), "/unlimited").Return(workspacesdk.WorkspaceFileInfo{Path: "/unlimited", Size: 4}, nil),
		conn.EXPECT().ReadFile(gomock.Any(), "/unlimited", int64(0), int64(0)).Return(
			io.NopCloser(strings.NewReader("all\n")), "text/plain", nil,
		),
		conn.EXPECT().ResolvePath(gomock.Any(), "/pipe").Return("/pipe", nil),
		conn.EXPECT().FileInfo(gomock.Any(), "/pipe").Return(workspacesdk.WorkspaceFileInfo{Path: "/pipe"}, nil),
		conn.EXPECT().ReadFile(gomock.Any(), "/pipe", int64(0), int64(0)).Return(
			nil, "", xerrors.New("path is not a regular file: named pipe"),
		),
	)

	result := readWorkspaceFilesV2(context.Background(), conn, []WorkspaceReadFileV2Args{
		{Path: "/bounded", Limit: 1},
		{Path: "/unlimited", Limit: 0},
		{Path: "/pipe", Limit: 0},
	}, 1<<20)
	require.Len(t, result.Files, 3)
	require.Equal(t, "one\n", result.Files[0].Content)
	require.Equal(t, 1, result.Files[0].LinesRead)
	require.False(t, result.Files[0].EndOfFile)
	require.Equal(t, "all\n", result.Files[1].Content)
	require.True(t, result.Files[1].EndOfFile)
	require.Empty(t, result.Files[2].Content)
	require.Contains(t, result.Files[2].Error, "not a regular file")
}

func TestWorkspaceFilesystemLimitSchemas(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t, []string{"workspace", "path", "limit"}, WorkspaceReadFileV2.Schema.Required)
	readLimit := WorkspaceReadFileV2.Schema.Properties["limit"].(map[string]any)
	require.EqualValues(t, 0, readLimit["minimum"])
	require.NotContains(t, readLimit, "default")
	require.NotContains(t, readLimit, "maximum")

	require.ElementsMatch(t, []string{"workspace", "path", "limit"}, WorkspaceListDirectoryV2.Schema.Required)
	directoryLimit := WorkspaceListDirectoryV2.Schema.Properties["limit"].(map[string]any)
	require.EqualValues(t, 0, directoryLimit["minimum"])
	require.NotContains(t, directoryLimit, "default")
	require.NotContains(t, directoryLimit, "maximum")

	files := WorkspaceReadFilesV2.Schema.Properties["files"].(map[string]any)
	item := files["items"].(map[string]any)
	require.ElementsMatch(t, []string{"path", "limit"}, item["required"])
	itemProperties := item["properties"].(map[string]any)
	perFileLimit := itemProperties["limit"].(map[string]any)
	require.EqualValues(t, 0, perFileLimit["minimum"])
	require.NotContains(t, perFileLimit, "default")
	require.NotContains(t, perFileLimit, "maximum")
}

func TestWorkspaceFilesystemAnnotations(t *testing.T) {
	t.Parallel()

	require.Equal(t, mcpDestructiveAnnotations, WorkspaceWriteFileV2.MCPAnnotations)
	require.Equal(t, mcpMutationIdempotentAnnotations, WorkspaceCreateDirectory.MCPAnnotations)
	require.Equal(t, mcpDestructiveAnnotations, WorkspaceMoveFile.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceReadFileV2.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceReadFilesV2.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceFileInfoTool.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceListDirectoryV2.MCPAnnotations)
}

func TestReadWorkspaceFileV2TextExplicitUnlimitedPreservesLiteralPayload(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/link").Return("/target", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/target").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/target",
		Size: 13,
	}, nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/link").Return(workspacesdk.WorkspaceFileInfo{
		Path:      "/link",
		IsSymlink: true,
	}, nil)
	conn.EXPECT().ReadFile(gomock.Any(), "/link", int64(0), int64(0)).Return(
		io.NopCloser(strings.NewReader("alpha\nbeta\n")), "text/plain", nil,
	)

	result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{Path: "/link", Limit: 0}, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "text", result.Encoding)
	require.True(t, result.IsSymlink)
	require.Equal(t, "/target", result.ResolvedPath)
	require.Equal(t, "alpha\nbeta\n", result.Content, "read_file must not inject line numbers or service headers")
	require.Equal(t, int64(13), result.FileSize)
	require.Equal(t, 2, result.TotalLines, "a trailing newline terminates the final line but does not create a phantom extra line")
	require.Equal(t, 2, result.LinesRead)
	require.True(t, result.EndOfFile)
}

func TestReadWorkspaceFileV2TextOffsetsNearBeginningMiddleAndEnd(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		offset     int64
		want       string
		nextOffset int64
		eof        bool
	}{
		{name: "beginning", offset: 1, want: "a\n", nextOffset: 2, eof: false},
		{name: "middle", offset: 2, want: "b\n", nextOffset: 3, eof: false},
		{name: "end", offset: 4, want: "d\n", nextOffset: 5, eof: true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			conn := agentconnmock.NewMockAgentConn(ctrl)
			conn.EXPECT().ResolvePath(gomock.Any(), "/file").Return("/file", nil)
			conn.EXPECT().FileInfo(gomock.Any(), "/file").Return(workspacesdk.WorkspaceFileInfo{
				Path: "/file",
				Size: 8,
			}, nil)
			conn.EXPECT().ReadFile(gomock.Any(), "/file", int64(0), int64(0)).Return(
				io.NopCloser(strings.NewReader("a\nb\nc\nd\n")), "text/plain", nil,
			)

			result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
				Path:   "/file",
				Offset: tc.offset,
				Limit:  1,
			}, 1<<20)
			require.NoError(t, err)
			require.Equal(t, tc.want, result.Content)
			require.Equal(t, 1, result.LinesRead)
			require.Equal(t, tc.nextOffset, result.NextOffset)
			require.Equal(t, tc.eof, result.EndOfFile)
		})
	}
}

func TestReadWorkspaceFileV2TextPaginationPreservesLineTerminators(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/file").Return("/file", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/file").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/file",
		Size: 6,
	}, nil)
	conn.EXPECT().ReadFile(gomock.Any(), "/file", int64(0), int64(0)).Return(
		io.NopCloser(strings.NewReader("a\nb\nc\n")), "text/plain", nil,
	)

	result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
		Path:   "/file",
		Offset: 2,
		Limit:  2,
	}, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "b\nc\n", result.Content)
	require.Equal(t, 2, result.LinesRead)
	require.Equal(t, int64(4), result.NextOffset)
	require.True(t, result.EndOfFile)
}
