package toolsdk

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestWorkspaceCapabilitiesSchema(t *testing.T) {
	t.Parallel()

	generic := WorkspaceCapabilities.Generic()
	require.NotNil(t, generic.Schema.Properties)
	require.Contains(t, generic.Schema.Properties, "workspace")
	require.Equal(t, []string{"workspace"}, generic.Schema.Required)
}

func TestReadWorkspaceCapabilities(t *testing.T) {
	t.Parallel()

	t.Run("ValidManifest", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		conn := agentconnmock.NewMockAgentConn(ctrl)
		payload := `{"schema_version":1,"profile":"developer-workspace","available_commands":["example"]}`
		conn.EXPECT().FileInfo(gomock.Any(), workspaceCapabilitiesPath).Return(workspacesdk.WorkspaceFileInfo{Size: int64(len(payload))}, nil)
		conn.EXPECT().ReadFile(gomock.Any(), workspaceCapabilitiesPath, int64(0), int64(maxWorkspaceCapabilitiesBytes)).Return(io.NopCloser(strings.NewReader(payload)), "application/json", nil)

		result, err := readWorkspaceCapabilities(context.Background(), conn)
		require.NoError(t, err)
		require.True(t, result.Available)
		require.JSONEq(t, payload, string(result.Manifest))
		require.Empty(t, result.Message)
	})

	t.Run("LegacyImageWithoutManifest", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		conn := agentconnmock.NewMockAgentConn(ctrl)
		conn.EXPECT().FileInfo(gomock.Any(), workspaceCapabilitiesPath).Return(workspacesdk.WorkspaceFileInfo{}, fs.ErrNotExist)

		result, err := readWorkspaceCapabilities(context.Background(), conn)
		require.NoError(t, err)
		require.False(t, result.Available)
		require.Nil(t, result.Manifest)
		require.Contains(t, result.Message, "does not publish")
	})

	t.Run("LegacyImageHTTPNotFound", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		conn := agentconnmock.NewMockAgentConn(ctrl)
		conn.EXPECT().FileInfo(gomock.Any(), workspaceCapabilitiesPath).Return(
			workspacesdk.WorkspaceFileInfo{},
			codersdk.NewTestError(http.StatusNotFound, http.MethodGet, workspaceCapabilitiesPath),
		)

		result, err := readWorkspaceCapabilities(context.Background(), conn)
		require.NoError(t, err)
		require.False(t, result.Available)
		require.Nil(t, result.Manifest)
		require.Contains(t, result.Message, "does not publish")
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		conn := agentconnmock.NewMockAgentConn(ctrl)
		payload := `{invalid`
		conn.EXPECT().FileInfo(gomock.Any(), workspaceCapabilitiesPath).Return(workspacesdk.WorkspaceFileInfo{Size: int64(len(payload))}, nil)
		conn.EXPECT().ReadFile(gomock.Any(), workspaceCapabilitiesPath, int64(0), int64(maxWorkspaceCapabilitiesBytes)).Return(io.NopCloser(strings.NewReader(payload)), "application/json", nil)

		_, err := readWorkspaceCapabilities(context.Background(), conn)
		require.ErrorContains(t, err, "not valid JSON")
	})
}
