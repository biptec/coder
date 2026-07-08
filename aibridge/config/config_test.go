package config_test

import (
	"testing"

	"github.com/coder/coder/v2/aibridge/config"
	"github.com/stretchr/testify/require"
)

func TestAWSBedrockValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      config.AWSBedrock
		errorMsg string
	}{
		{
			name: "invoke model valid",
			cfg: config.AWSBedrock{
				Region:         "us-east-1",
				Model:          "anthropic.claude-sonnet",
				SmallFastModel: "anthropic.claude-haiku",
			},
		},
		{
			name: "invoke model valid with base url instead of region",
			cfg: config.AWSBedrock{
				BaseURL:        "https://bedrock-runtime.example.com",
				Model:          "anthropic.claude-sonnet",
				SmallFastModel: "anthropic.claude-haiku",
			},
		},
		{
			name: "invoke model missing region and base url",
			cfg: config.AWSBedrock{
				Model:          "anthropic.claude-sonnet",
				SmallFastModel: "anthropic.claude-haiku",
			},
			errorMsg: "region or base url required",
		},
		{
			name: "invoke model missing model",
			cfg: config.AWSBedrock{
				Region:         "us-east-1",
				SmallFastModel: "anthropic.claude-haiku",
			},
			errorMsg: "model required",
		},
		{
			name: "invoke model missing small fast model",
			cfg: config.AWSBedrock{
				Region: "us-east-1",
				Model:  "anthropic.claude-sonnet",
			},
			errorMsg: "small fast model required",
		},
		{
			name: "unknown endpoint allows empty fields",
			cfg: config.AWSBedrock{
				Endpoint: config.BedrockEndpoint("unknown"),
			},
		},
		{
			name: "mantle valid official api prefix",
			cfg: config.AWSBedrock{
				Region:   "us-east-1",
				BaseURL:  "https://bedrock-mantle.us-east-1.api.aws/anthropic",
				Endpoint: config.BedrockEndpointMantle,
			},
		},
		{
			name: "mantle valid proxy api prefix",
			cfg: config.AWSBedrock{
				Region:   "us-east-1",
				BaseURL:  "https://proxy.internal/litellm",
				Endpoint: config.BedrockEndpointMantle,
			},
		},
		{
			name: "mantle missing region",
			cfg: config.AWSBedrock{
				BaseURL:  "https://bedrock-mantle.us-east-1.api.aws",
				Endpoint: config.BedrockEndpointMantle,
			},
			errorMsg: "region required",
		},
		{
			name: "mantle missing base url",
			cfg: config.AWSBedrock{
				Region:   "us-east-1",
				Endpoint: config.BedrockEndpointMantle,
			},
			errorMsg: "base_url required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if tt.errorMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
				return
			}
			require.NoError(t, err)
		})
	}
}
