package toolsdk_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestWorkspaceExecObservationContract(t *testing.T) {
	t.Parallel()

	require.NotContains(t, toolsdk.WorkspaceExec.Schema.Properties, "timeout_ms")
	require.Contains(t, toolsdk.WorkspaceExec.Description, "no process execution timeout")
	require.Contains(t, toolsdk.WorkspaceExec.Description, "single shared 60-second observation budget")
	require.Contains(t, toolsdk.WorkspaceExec.Description, "process_id")
	require.Contains(t, toolsdk.WorkspaceExec.Description, "running=true")
	require.Contains(t, toolsdk.WorkspaceExec.Description, "continues independently")
	require.Contains(t, toolsdk.WorkspaceExec.Description, "coder_workspace_process_output")
}

func TestInvocationToolContext(t *testing.T) {
	t.Parallel()

	ctx := toolsdk.WithInvocationTool(context.Background(), "  exec  ")
	require.Equal(t, "exec", toolsdk.InvocationToolFromContext(ctx))
	require.Empty(t, toolsdk.InvocationToolFromContext(context.Background()))
}
