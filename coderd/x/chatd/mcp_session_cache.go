package chatd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/coderd/x/chatd/mcpclient"
)

type mcpSessionFingerprintInput struct {
	Configs      []database.MCPServerConfig    `json:"configs"`
	Tokens       []database.MCPServerUserToken `json:"tokens"`
	OwnerID      uuid.UUID                     `json:"owner_id"`
	CoderHeaders map[string]string             `json:"coder_headers"`
}

type mcpChatSession struct {
	fingerprint [sha256.Size]byte
	tools       []fantasy.AgentTool
	cleanup     func()
}

type mcpSessionCache struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]mcpChatSession
}

func newMCPSessionCache() *mcpSessionCache {
	return &mcpSessionCache{sessions: make(map[uuid.UUID]mcpChatSession)}
}

func mcpSessionFingerprint(
	configs []database.MCPServerConfig,
	tokens []database.MCPServerUserToken,
	ownerID uuid.UUID,
	coderHeaders map[string]string,
) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(mcpSessionFingerprintInput{
		Configs:      configs,
		Tokens:       tokens,
		OwnerID:      ownerID,
		CoderHeaders: coderHeaders,
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func (c *mcpSessionCache) connect(
	chatID uuid.UUID,
	fingerprint [sha256.Size]byte,
	connect func() ([]fantasy.AgentTool, func()),
) []fantasy.AgentTool {
	c.mu.Lock()
	if session, ok := c.sessions[chatID]; ok && session.fingerprint == fingerprint {
		c.mu.Unlock()
		return session.tools
	}
	stale, hadStale := c.sessions[chatID]
	if hadStale {
		delete(c.sessions, chatID)
	}
	c.mu.Unlock()

	if hadStale && stale.cleanup != nil {
		stale.cleanup()
	}

	tools, cleanup := connect()
	if cleanup == nil {
		cleanup = func() {}
	}
	if len(tools) == 0 {
		cleanup()
		return nil
	}

	c.mu.Lock()
	if current, ok := c.sessions[chatID]; ok {
		if current.fingerprint == fingerprint {
			c.mu.Unlock()
			cleanup()
			return current.tools
		}
		delete(c.sessions, chatID)
		c.mu.Unlock()
		if current.cleanup != nil {
			current.cleanup()
		}
		c.mu.Lock()
	}
	c.sessions[chatID] = mcpChatSession{
		fingerprint: fingerprint,
		tools:       tools,
		cleanup:     cleanup,
	}
	c.mu.Unlock()
	return tools
}

func (c *mcpSessionCache) close(chatID uuid.UUID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	session, ok := c.sessions[chatID]
	if ok {
		delete(c.sessions, chatID)
	}
	c.mu.Unlock()
	if ok && session.cleanup != nil {
		session.cleanup()
	}
}

func (c *mcpSessionCache) closeAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	sessions := c.sessions
	c.sessions = make(map[uuid.UUID]mcpChatSession)
	c.mu.Unlock()
	for _, session := range sessions {
		if session.cleanup != nil {
			session.cleanup()
		}
	}
}

func canCacheMCPSession(configs []database.MCPServerConfig) bool {
	if len(configs) == 0 {
		return false
	}
	for _, cfg := range configs {
		if cfg.Transport != "" && cfg.Transport != "streamable_http" {
			return false
		}
	}
	return true
}

func (server *Server) connectExternalMCPForChat(
	ctx context.Context,
	logger slog.Logger,
	chat database.Chat,
	configs []database.MCPServerConfig,
	tokens []database.MCPServerUserToken,
) ([]fantasy.AgentTool, func(), error) {
	coderHeaders := chatprovider.CoderHeaders(chat)
	fingerprint, err := mcpSessionFingerprint(configs, tokens, chat.OwnerID, coderHeaders)
	if err != nil {
		return nil, nil, err
	}
	connect := func() ([]fantasy.AgentTool, func()) {
		return mcpclient.ConnectAll(
			ctx,
			logger,
			configs,
			tokens,
			chat.OwnerID,
			server.oidcTokenSource,
			coderHeaders,
		)
	}
	if server.mcpSessions == nil || !canCacheMCPSession(configs) {
		tools, cleanup := connect()
		return tools, cleanup, nil
	}
	tools := server.mcpSessions.connect(chat.ID, fingerprint, connect)
	return tools, func() {}, nil
}

func (server *Server) closeExternalMCPForChat(chatID uuid.UUID) {
	if server == nil || server.mcpSessions == nil {
		return
	}
	server.mcpSessions.close(chatID)
}
