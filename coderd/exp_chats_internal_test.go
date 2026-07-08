package coderd

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// TestResolveChatStartVersion covers the RequireActiveVersion
// auto-update behavior for chat-initiated workspace starts, including
// the DEREM-16/17 regression cases previously covered end-to-end.
func TestResolveChatStartVersion(t *testing.T) {
	t.Parallel()

	v1 := uuid.New()
	v2 := uuid.New()

	tests := []struct {
		name          string
		transition    codersdk.WorkspaceTransition
		latestVersion uuid.UUID
		activeVersion uuid.UUID
		wantVersion   uuid.UUID
		wantUpdated   bool
	}{
		{
			name:          "StartVersionMismatchBumps",
			transition:    codersdk.WorkspaceTransitionStart,
			latestVersion: v1,
			activeVersion: v2,
			wantVersion:   v2,
			wantUpdated:   true,
		},
		{
			name:          "StartVersionMatchPinsWithoutUpdate",
			transition:    codersdk.WorkspaceTransitionStart,
			latestVersion: v1,
			activeVersion: v1,
			wantVersion:   v1,
			wantUpdated:   false,
		},
		{
			name:          "StopIgnoresActiveVersion",
			transition:    codersdk.WorkspaceTransitionStop,
			latestVersion: v1,
			activeVersion: v2,
			wantVersion:   uuid.Nil,
			wantUpdated:   false,
		},
		{
			name:          "DeleteIgnoresActiveVersion",
			transition:    codersdk.WorkspaceTransitionDelete,
			latestVersion: v1,
			activeVersion: v2,
			wantVersion:   uuid.Nil,
			wantUpdated:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := codersdk.CreateWorkspaceBuildRequest{Transition: tt.transition}
			got, updated := resolveChatStartVersion(req, tt.latestVersion, tt.activeVersion)
			require.Equal(t, tt.wantVersion, got.TemplateVersionID)
			require.Equal(t, tt.wantUpdated, updated)
			require.Equal(t, tt.transition, got.Transition)
		})
	}
}

func TestValidateChatModelProviderOptions_AnthropicThinkingDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		display string
		wantErr string
	}{
		{name: "Summarized", display: "summarized"},
		{name: "Omitted", display: " omitted "},
		{name: "Empty", display: " "},
		{
			name:    "Invalid",
			display: "summrized",
			wantErr: "provider_options.anthropic.thinking_display must be one of summarized, omitted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			display := tt.display
			err := validateChatModelProviderOptions(&codersdk.ChatModelProviderOptions{
				Anthropic: &codersdk.ChatModelAnthropicProviderOptions{
					ThinkingDisplay: &display,
				},
			})
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateChatModelConfigProviderModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		model      string
		provider   database.AIProvider
		wantErr    bool
		wantDetail string
	}{
		{
			name:  "OpenRouterNameWithOpenAITypeAndSlashModel",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name: "openrouter",
				Type: database.AIProviderTypeOpenai,
			},
			wantErr:    true,
			wantDetail: "Change the AI provider type to openrouter or openai-compat.",
		},
		{
			name:  "OpenRouterNameWithWhitespaceAndCase",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name: " OpenRouter ",
				Type: database.AIProviderTypeOpenai,
			},
			wantErr:    true,
			wantDetail: "Change the AI provider type to openrouter or openai-compat.",
		},
		{
			name:  "OpenRouterHostWithOpenAITypeAndSlashModel",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name:    "private-relay",
				Type:    database.AIProviderTypeOpenai,
				BaseUrl: "https://openrouter.ai/api/v1",
			},
			wantErr:    true,
			wantDetail: "Change the AI provider type to openrouter or openai-compat.",
		},
		{
			name:  "OpenRouterHostWithPort",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name:    "private-relay",
				Type:    database.AIProviderTypeOpenai,
				BaseUrl: "https://openrouter.ai:443/api/v1",
			},
			wantErr:    true,
			wantDetail: "Change the AI provider type to openrouter or openai-compat.",
		},
		{
			name:  "OpenRouterSubdomainWithOpenAIType",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name:    "private-relay",
				Type:    database.AIProviderTypeOpenai,
				BaseUrl: "https://api.openrouter.ai/v1",
			},
			wantErr:    true,
			wantDetail: "Change the AI provider type to openrouter or openai-compat.",
		},
		{
			name:  "OpenRouterTypeAllowsSlashModel",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name: "openrouter",
				Type: database.AIProviderTypeOpenrouter,
			},
		},
		{
			name:  "OpenAICompatTypeAllowsSlashModel",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name: "openrouter",
				Type: database.AIProviderTypeOpenaiCompat,
			},
		},
		{
			name:  "PrivateOpenAIProxyAllowsSlashModel",
			model: "anthropic/claude-opus-4.6",
			provider: database.AIProvider{
				Name:    "private-relay",
				Type:    database.AIProviderTypeOpenai,
				BaseUrl: "https://llm-relay.internal/v1",
			},
		},
		{
			name:  "OpenRouterNameWithPlainModelAllowed",
			model: "gpt-4.1",
			provider: database.AIProvider{
				Name: "openrouter",
				Type: database.AIProviderTypeOpenai,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := validateChatModelConfigProviderModel(tt.provider, tt.model)
			if tt.wantErr {
				require.NotNil(t, got)
				require.Contains(t, got.Response.Detail, tt.wantDetail)
				return
			}
			require.Nil(t, got)
		})
	}
}

func TestRewriteChatStartWorkspaceManualUpdateResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		resp           codersdk.Response
		fallbackDetail string
		wantDetail     string
	}{
		{
			name: "NoValidationsAndEmptyDetail",
			resp: codersdk.Response{
				Message: "missing required parameter",
			},
			fallbackDetail: "wrapped missing required parameter",
			wantDetail:     "missing required parameter",
		},
		{
			name: "NoValidationsAndExistingDetail",
			resp: codersdk.Response{
				Message: "missing required parameter",
				Detail:  "region must be set before the workspace can start",
			},
			fallbackDetail: "wrapped missing required parameter",
			wantDetail:     "missing required parameter: region must be set before the workspace can start",
		},
		{
			name: "ValidationsAndEmptyDetail",
			resp: codersdk.Response{
				Message: "missing required parameter",
				Validations: []codersdk.ValidationError{{
					Field:  "region",
					Detail: "region must be set before the workspace can start",
				}},
			},
			fallbackDetail: "wrapped missing required parameter",
			wantDetail:     "wrapped missing required parameter",
		},
		{
			name: "ValidationsAndExistingDetail",
			resp: codersdk.Response{
				Message: "missing required parameter",
				Detail:  "region must be set before the workspace can start",
				Validations: []codersdk.ValidationError{{
					Field:  "region",
					Detail: "region must be set before the workspace can start",
				}},
			},
			fallbackDetail: "wrapped missing required parameter",
			wantDetail:     "region must be set before the workspace can start",
		},
	}

	const retryInstructions = "Use read_template before retrying start_workspace."
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rewriteChatStartWorkspaceManualUpdateResponse(tt.resp, tt.fallbackDetail, retryInstructions)
			require.Equal(t, retryInstructions, got.Message)
			require.Equal(t, tt.wantDetail, got.Detail)
			require.Equal(t, tt.resp.Validations, got.Validations)
		})
	}
}
