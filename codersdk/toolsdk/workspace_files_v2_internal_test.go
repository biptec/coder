package toolsdk

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestReadWorkspaceFileV2BinaryUsesResolvedTargetSize(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/link").Return("/target", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/target").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/target",
		Size: 6,
	}, nil)
	conn.EXPECT().ReadFile(gomock.Any(), "/link", int64(0), int64(4)).Return(
		io.NopCloser(strings.NewReader("abcd")), "application/octet-stream", nil,
	)

	result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{
		Path:   "/link",
		Binary: true,
		Limit:  4,
	})
	require.NoError(t, err)
	require.Equal(t, int64(6), result.FileSize)
	require.Equal(t, int64(4), result.NextOffset)
	require.False(t, result.EndOfFile, "EOF must be based on the resolved target size, not symlink metadata")
}

func TestWorkspaceFilesystemAnnotations(t *testing.T) {
	t.Parallel()

	require.Equal(t, mcpDestructiveIdempotentAnnotations, WorkspaceWriteFileV2.MCPAnnotations)
	require.Equal(t, mcpMutationIdempotentAnnotations, WorkspaceCreateDirectory.MCPAnnotations)
	require.Equal(t, mcpDestructiveAnnotations, WorkspaceMoveFile.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceReadFileV2.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceReadFilesV2.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceFileInfoTool.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceListDirectoryV2.MCPAnnotations)
}

func TestReadWorkspaceFileV2TextPreservesLiteralPayloadAndHasNoDefaultLimit(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ResolvePath(gomock.Any(), "/link").Return("/target", nil)
	conn.EXPECT().FileInfo(gomock.Any(), "/target").Return(workspacesdk.WorkspaceFileInfo{
		Path: "/target",
		Size: 13,
	}, nil)
	conn.EXPECT().ReadFile(gomock.Any(), "/link", int64(0), int64(0)).Return(
		io.NopCloser(strings.NewReader("alpha\nbeta\n")), "text/plain", nil,
	)

	result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{Path: "/link"})
	require.NoError(t, err)
	require.Equal(t, "text", result.Encoding)
	require.Equal(t, "alpha\nbeta\n", result.Content, "read_file must not inject line numbers or service headers")
	require.Equal(t, int64(13), result.FileSize)
	require.Equal(t, 2, result.TotalLines, "a trailing newline terminates the final line but does not create a phantom extra line")
	require.Equal(t, 2, result.LinesRead)
	require.True(t, result.EndOfFile)
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
	})
	require.NoError(t, err)
	require.Equal(t, "b\nc\n", result.Content)
	require.Equal(t, 2, result.LinesRead)
	require.Equal(t, int64(4), result.NextOffset)
	require.True(t, result.EndOfFile)
}
