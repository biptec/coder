package chatd

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
)

func cachedTestTool(name string) fantasy.AgentTool {
	return fantasy.NewAgentTool[struct{}](name, "test", func(context.Context, struct{}, fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	})
}

func TestMCPSessionCacheReusesWithinChat(t *testing.T) {
	t.Parallel()

	cache := newMCPSessionCache()
	chatID := uuid.New()
	fingerprint := [32]byte{1}
	connects := 0
	cleanups := 0
	connect := func() ([]fantasy.AgentTool, func()) {
		connects++
		return []fantasy.AgentTool{cachedTestTool("cached")}, func() { cleanups++ }
	}

	first := cache.connect(chatID, fingerprint, connect)
	second := cache.connect(chatID, fingerprint, connect)
	require.Equal(t, 1, connects)
	require.Equal(t, 0, cleanups)
	require.Same(t, first[0], second[0])

	cache.close(chatID)
	require.Equal(t, 1, cleanups)
}

func TestMCPSessionCacheSeparatesChats(t *testing.T) {
	t.Parallel()

	cache := newMCPSessionCache()
	fingerprint := [32]byte{2}
	connects := 0
	connect := func() ([]fantasy.AgentTool, func()) {
		connects++
		return []fantasy.AgentTool{cachedTestTool("cached")}, func() {}
	}

	cache.connect(uuid.New(), fingerprint, connect)
	cache.connect(uuid.New(), fingerprint, connect)
	require.Equal(t, 2, connects)
	cache.closeAll()
}

func TestMCPSessionCacheReconnectsOnFingerprintChange(t *testing.T) {
	t.Parallel()

	cache := newMCPSessionCache()
	chatID := uuid.New()
	connects := 0
	cleanups := 0
	connect := func() ([]fantasy.AgentTool, func()) {
		connects++
		return []fantasy.AgentTool{cachedTestTool("cached")}, func() { cleanups++ }
	}

	cache.connect(chatID, [32]byte{3}, connect)
	cache.connect(chatID, [32]byte{4}, connect)
	require.Equal(t, 2, connects)
	require.Equal(t, 1, cleanups)
	cache.closeAll()
	require.Equal(t, 2, cleanups)
}

func TestCanCacheMCPSession(t *testing.T) {
	t.Parallel()

	require.True(t, canCacheMCPSession([]database.MCPServerConfig{{Transport: "streamable_http"}, {Transport: ""}}))
	require.False(t, canCacheMCPSession([]database.MCPServerConfig{{Transport: "sse"}}))
	require.False(t, canCacheMCPSession(nil))
}
