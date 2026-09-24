//nolint:testpackage // This test exercises internal semantic HTTP error normalization.
package workspacesdk

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSemanticResponseError(t *testing.T) {
	t.Parallel()

	t.Run("typed semantic error", func(t *testing.T) {
		t.Parallel()
		err := semanticResponseError(http.StatusBadRequest, []byte(`{"code":"unsupported_language","message":"Go only","detail":"detected Python"}`))
		var semanticErr *SemanticError
		require.ErrorAs(t, err, &semanticErr)
		require.Equal(t, "unsupported_language", semanticErr.Code)
		require.Equal(t, "Go only", semanticErr.Message)
		require.Equal(t, "detected Python", semanticErr.Detail)
	})

	t.Run("old workspace agent", func(t *testing.T) {
		t.Parallel()
		err := semanticResponseError(http.StatusNotFound, []byte("404 page not found\n"))
		var semanticErr *SemanticError
		require.ErrorAs(t, err, &semanticErr)
		require.Equal(t, "semantic_backend_unavailable", semanticErr.Code)
		require.Contains(t, semanticErr.Message, "Workspace Agent is outdated")
		require.Contains(t, semanticErr.Message, "restart the workspace")
	})

	t.Run("ordinary untyped error stays ordinary", func(t *testing.T) {
		t.Parallel()
		err := semanticResponseError(http.StatusInternalServerError, []byte("upstream exploded"))
		var semanticErr *SemanticError
		require.False(t, errors.As(err, &semanticErr))
		require.Contains(t, err.Error(), "HTTP 500")
		require.Contains(t, err.Error(), "upstream exploded")
	})
}
