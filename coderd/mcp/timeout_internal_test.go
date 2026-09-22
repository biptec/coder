package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestMCPFromSDKUsesConfiguredToolTimeout(t *testing.T) {
	t.Parallel()

	deps, err := toolsdk.NewDeps(nil, toolsdk.WithMCPToolTimeoutMax(25*time.Millisecond))
	require.NoError(t, err)

	sdkTool := toolsdk.GenericTool{
		Tool: aisdk.Tool{
			Name:        "timeout_probe",
			Description: "timeout probe",
			Schema: aisdk.Schema{
				Properties: map[string]any{},
			},
		},
		Handler: func(ctx context.Context, _ toolsdk.Deps, _ json.RawMessage) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	tool := mcpFromSDK(sdkTool, deps)
	started := time.Now()
	_, err = tool.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "timeout_probe",
		Arguments: map[string]any{},
	}})
	elapsed := time.Since(started)

	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
	require.Less(t, elapsed, time.Second)
	require.GreaterOrEqual(t, elapsed, 15*time.Millisecond)
}
