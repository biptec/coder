package toolsdk

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
)

func TestObserveProvisionerLogsCursorAndLimit(t *testing.T) {
	t.Parallel()

	zeroWait := 0
	result, err := observeProvisionerLogs(context.Background(), newMCPObservationBudget(), 10, &zeroWait, 2, func(_ context.Context, after int64) ([]codersdk.ProvisionerJobLog, error) {
		require.Equal(t, int64(10), after)
		return []codersdk.ProvisionerJobLog{
			{ID: 11, Output: "one"},
			{ID: 12, Output: "two"},
			{ID: 13, Output: "three"},
		}, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, result.Logs)
	require.Equal(t, int64(12), result.NextCursor)
	require.True(t, result.HasMore)
	require.False(t, result.Complete)
}

func TestObserveProvisionerLogsSnapshotFitsWithinLimit(t *testing.T) {
	t.Parallel()

	zeroWait := 0
	result, err := observeProvisionerLogs(context.Background(), newMCPObservationBudget(), 20, &zeroWait, 10, func(_ context.Context, after int64) ([]codersdk.ProvisionerJobLog, error) {
		require.Equal(t, int64(20), after)
		return []codersdk.ProvisionerJobLog{{ID: 21, Output: "done"}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"done"}, result.Logs)
	require.Equal(t, int64(21), result.NextCursor)
	require.False(t, result.HasMore)
	require.False(t, result.Complete, "job state, not the log snapshot alone, determines completion")
}

func TestObserveProvisionerLogsReturnsAtWaitBoundary(t *testing.T) {
	t.Parallel()

	waitMs := 20
	calls := 0
	started := time.Now()
	result, err := observeProvisionerLogs(context.Background(), newMCPObservationBudget(), 7, &waitMs, 0, func(_ context.Context, after int64) ([]codersdk.ProvisionerJobLog, error) {
		require.Equal(t, int64(7), after)
		calls++
		return nil, nil
	})
	require.NoError(t, err)
	require.Empty(t, result.Logs)
	require.Equal(t, int64(7), result.NextCursor)
	require.False(t, result.Complete)
	require.GreaterOrEqual(t, calls, 1)
	require.Less(t, time.Since(started), time.Second)
}

func TestSetProvisionerLogJobState(t *testing.T) {
	t.Parallel()

	result := ProvisionerLogObservationResult{}
	setProvisionerLogJobState(&result, codersdk.ProvisionerJob{Status: codersdk.ProvisionerJobRunning})
	require.Equal(t, codersdk.ProvisionerJobRunning, result.JobStatus)
	require.False(t, result.Complete)

	setProvisionerLogJobState(&result, codersdk.ProvisionerJob{Status: codersdk.ProvisionerJobSucceeded})
	require.Equal(t, codersdk.ProvisionerJobSucceeded, result.JobStatus)
	require.True(t, result.Complete)

	setProvisionerLogJobState(&result, codersdk.ProvisionerJob{Status: codersdk.ProvisionerJobFailed})
	require.Equal(t, codersdk.ProvisionerJobFailed, result.JobStatus)
	require.True(t, result.Complete)

	setProvisionerLogJobState(&result, codersdk.ProvisionerJob{Status: codersdk.ProvisionerJobCanceled})
	require.Equal(t, codersdk.ProvisionerJobCanceled, result.JobStatus)
	require.True(t, result.Complete)

	result.HasMore = true
	setProvisionerLogJobState(&result, codersdk.ProvisionerJob{Status: codersdk.ProvisionerJobSucceeded})
	require.False(t, result.Complete)
}

func TestProvisionerLogToolSchemasExposeBoundedObservation(t *testing.T) {
	t.Parallel()

	for _, tool := range []struct {
		name        string
		description string
		properties  map[string]any
	}{
		{name: GetWorkspaceBuildLogs.Name, description: GetWorkspaceBuildLogs.Description, properties: GetWorkspaceBuildLogs.Schema.Properties},
		{name: GetTemplateVersionLogs.Name, description: GetTemplateVersionLogs.Description, properties: GetTemplateVersionLogs.Schema.Properties},
	} {
		require.Contains(t, tool.description, "at most a 60-second observation budget", tool.name)
		require.Contains(t, tool.description, "never cancels", tool.name)
		require.Contains(t, tool.description, "non-follow log snapshots", tool.name)
		require.Contains(t, tool.description, "complete=true", tool.name)
		require.Contains(t, tool.description, "job_status", tool.name)
		require.Contains(t, tool.description, "has_more=true", tool.name)
		require.Contains(t, tool.description, "cursor=next_cursor", tool.name)
		require.Contains(t, tool.properties, "cursor", tool.name)
		require.Contains(t, tool.properties, "wait_timeout_ms", tool.name)
		require.Contains(t, tool.properties, "limit", tool.name)
		waitSchema := tool.properties["wait_timeout_ms"].(map[string]any)
		require.Equal(t, 10000, waitSchema["default"], tool.name)
		require.Equal(t, 60000, waitSchema["maximum"], tool.name)
	}
}

func TestProvisionerLogObservationValidation(t *testing.T) {
	t.Parallel()

	fetch := func(context.Context, int64) ([]codersdk.ProvisionerJobLog, error) {
		t.Fatal("snapshot must not be fetched for invalid input")
		return nil, nil
	}

	_, err := observeProvisionerLogs(context.Background(), newMCPObservationBudget(), -1, nil, 0, fetch)
	require.ErrorContains(t, err, "cursor cannot be negative")

	tooLong := int(mcpToolObservationWindow.Milliseconds()) + 1
	_, err = observeProvisionerLogs(context.Background(), newMCPObservationBudget(), 0, &tooLong, 0, fetch)
	require.ErrorContains(t, err, "cannot exceed")

	_, err = observeProvisionerLogs(context.Background(), newMCPObservationBudget(), 0, nil, maxProvisionerLogLimit+1, fetch)
	require.ErrorContains(t, err, "limit must be between")
}
