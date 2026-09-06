package appearance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
)

func TestDefaultFetcherWorkspaceActivityNowThreshold(t *testing.T) {
	t.Parallel()

	t.Run("Default", func(t *testing.T) {
		t.Parallel()
		fetcher := NewDefaultFetcher("", codersdk.DefaultWorkspaceActivityNowThreshold, false)
		cfg, err := fetcher.Fetch(context.Background())
		require.NoError(t, err)
		require.Equal(t, codersdk.DefaultWorkspaceActivityNowThreshold.Milliseconds(), cfg.WorkspaceActivityNowThresholdMS)
	})

	t.Run("Custom", func(t *testing.T) {
		t.Parallel()
		fetcher := NewDefaultFetcher("", 12*time.Minute, true)
		cfg, err := fetcher.Fetch(context.Background())
		require.NoError(t, err)
		require.Equal(t, (12 * time.Minute).Milliseconds(), cfg.WorkspaceActivityNowThresholdMS)
		require.True(t, cfg.WorkspaceVolumeCopyEnabled)
	})
}
