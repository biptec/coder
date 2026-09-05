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

func TestReadWorkspaceFileV2TextUsesLineReaderDirectly(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().ReadFileLines(gomock.Any(), "/link", int64(1), int64(200), gomock.Any()).Return(
		workspacesdk.ReadFileLinesResponse{
			Success:    true,
			FileSize:   7,
			TotalLines: 1,
			LinesRead:  1,
			Content:    "1\tpayload",
		}, nil,
	)

	result, err := readWorkspaceFileV2(context.Background(), conn, WorkspaceReadFileV2Args{Path: "/link"})
	require.NoError(t, err)
	require.Equal(t, "text", result.Encoding)
	require.Equal(t, "1\tpayload", result.Content)
	require.Equal(t, int64(7), result.FileSize)
	require.True(t, result.EndOfFile)
}
