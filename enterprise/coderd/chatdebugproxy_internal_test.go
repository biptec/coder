package coderd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/codersdk"
)

func TestChatDebugProxyURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		reqURL  string
		want    string
	}{
		{
			name:    "bare host and port",
			address: "http://10.0.0.1:8080",
			reqURL:  "/api/experimental/chats/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/debug/snapshot",
			want:    "http://10.0.0.1:8080/api/experimental/chats/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/debug/snapshot",
		},
		{
			name:    "address with base path is preserved, not overwritten",
			address: "http://10.0.0.1:8080/coder",
			reqURL:  "/api/experimental/chats/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/debug/snapshot",
			want:    "http://10.0.0.1:8080/coder/api/experimental/chats/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/debug/snapshot",
		},
		{
			name:    "query string is forwarded",
			address: "http://10.0.0.1:8080",
			reqURL:  "/api/experimental/chats/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/debug/snapshot?foo=bar",
			want:    "http://10.0.0.1:8080/api/experimental/chats/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/debug/snapshot?foo=bar",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			baseURL, err := url.Parse(tt.address)
			require.NoError(t, err)
			reqURL, err := url.Parse(tt.reqURL)
			require.NoError(t, err)

			got := chatDebugProxyURL(baseURL, reqURL)
			require.Equal(t, tt.want, got.String())
		})
	}
}

func TestChatDebugProxyHandler_ProxyErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resolve      func(context.Context, uuid.UUID) (string, bool)
		ignoreErrors bool
	}{
		{
			name:    "replica not found",
			resolve: func(context.Context, uuid.UUID) (string, bool) { return "", false },
		},
		{
			name:         "replica unreachable",
			resolve:      func(context.Context, uuid.UUID) (string, bool) { return "http://127.0.0.1:0", true },
			ignoreErrors: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := chatDebugProxyHandler(slogtest.Make(t, &slogtest.Options{IgnoreErrors: tt.ignoreErrors}), http.DefaultClient, tt.resolve)

			rw := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/experimental/chats/"+uuid.NewString()+"/debug/snapshot", nil)
			handler(rw, req, uuid.New())

			require.Equal(t, http.StatusBadGateway, rw.Code)
		})
	}
}

func TestChatDebugProxyHandler_ForwardsVerbatim(t *testing.T) {
	t.Parallel()

	var (
		gotForwarded     string
		gotSessionToken  string
		upstreamRequests int
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		upstreamRequests++
		gotForwarded = r.Header.Get(coderd.ChatDebugForwardedHeader)
		gotSessionToken = r.Header.Get(codersdk.SessionTokenHeader)
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"execution_state":"R0"}`))
	}))
	defer upstream.Close()

	resolve := func(context.Context, uuid.UUID) (string, bool) { return upstream.URL, true }
	handler := chatDebugProxyHandler(slogtest.Make(t, nil), upstream.Client(), resolve)

	rw := httptest.NewRecorder()
	chatID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/experimental/chats/"+chatID.String()+"/debug/snapshot?foo=bar", nil)
	req.Header.Set(codersdk.SessionTokenHeader, "test-token")
	handler(rw, req, uuid.New())

	require.Equal(t, 1, upstreamRequests)
	assert.Equal(t, "1", gotForwarded)
	assert.Equal(t, "test-token", gotSessionToken)

	require.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"execution_state":"R0"}`, rw.Body.String())
}

func TestChatDebugProxyHandler_CapsResponseBody(t *testing.T) {
	t.Parallel()

	const overLimit = (1 << 20) + 1024
	upstream := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(make([]byte, overLimit))
	}))
	defer upstream.Close()

	resolve := func(context.Context, uuid.UUID) (string, bool) { return upstream.URL, true }
	handler := chatDebugProxyHandler(slogtest.Make(t, nil), upstream.Client(), resolve)

	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/experimental/chats/"+uuid.NewString()+"/debug/snapshot", nil)
	handler(rw, req, uuid.New())

	require.Equal(t, http.StatusOK, rw.Code)
	require.Equal(t, 1<<20, rw.Body.Len())
}
