package toolsdk

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
)

func TestWorkspaceAgentToolError(t *testing.T) {
	t.Parallel()

	t.Run("plain route not found becomes restart hint", func(t *testing.T) {
		t.Parallel()
		err := codersdk.ReadBodyAsError(&http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader("404 page not found\n")),
		})
		got := workspaceAgentToolError(err)
		require.EqualError(t, got, outdatedWorkspaceAgentToolMessage)
	})

	t.Run("json resource not found is preserved", func(t *testing.T) {
		t.Parallel()
		err := codersdk.ReadBodyAsError(&http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"parent directory does not exist"}`)),
		})
		require.Same(t, err, workspaceAgentToolError(err))
		require.Contains(t, err.Error(), "parent directory does not exist")
	})

	t.Run("other status is preserved", func(t *testing.T) {
		t.Parallel()
		err := codersdk.ReadBodyAsError(&http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader("boom\n")),
		})
		require.Same(t, err, workspaceAgentToolError(err))
	})
}
