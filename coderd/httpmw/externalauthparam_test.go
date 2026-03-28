package httpmw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/externalauth"
	"github.com/coder/coder/v2/coderd/httpmw"
)

//nolint:bodyclose
func TestExternalAuthParam(t *testing.T) {
	t.Parallel()
	t.Run("Found", func(t *testing.T) {
		t.Parallel()
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("externalauth", "my-id")
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
		res := httptest.NewRecorder()

		httpmw.ExtractExternalAuthParam(func(id string) (*externalauth.Config, bool) {
			if id == "my-id" {
				return &externalauth.Config{ID: "my-id"}, true
			}
			return nil, false
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "my-id", httpmw.ExternalAuthParam(r).ID)
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(res, r)

		require.Equal(t, http.StatusOK, res.Result().StatusCode)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("externalauth", "my-id")
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
		res := httptest.NewRecorder()

		httpmw.ExtractExternalAuthParam(func(string) (*externalauth.Config, bool) {
			return nil, false
		})(nil).ServeHTTP(res, r)

		require.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}
