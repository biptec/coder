package toolsdk_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestWorkspaceExecHasNoExecutionTimeout(t *testing.T) {
	t.Parallel()

	require.NotContains(t, toolsdk.WorkspaceExec.Schema.Properties, "timeout_ms")
	require.Contains(t, toolsdk.WorkspaceExec.Description, "no execution timeout")
}

func TestInvocationToolContext(t *testing.T) {
	t.Parallel()

	ctx := toolsdk.WithInvocationTool(context.Background(), "  exec  ")
	require.Equal(t, "exec", toolsdk.InvocationToolFromContext(ctx))
	require.Empty(t, toolsdk.InvocationToolFromContext(context.Background()))
}
