//go:build !slim

package cli_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/cli"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestSyncAndLoadExternalAuthProviders(t *testing.T) {
	t.Parallel()

	t.Run("UpsertsAndLoadsEnvProviders", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		logger := slogtest.Make(t, nil)
		ctx := testutil.Context(t, testutil.WaitShort)
		//nolint:gocritic // Unit test needs system context for dbauthz.
		ctx = dbauthz.AsSystemRestricted(ctx)

		envProviders := []codersdk.ExternalAuthConfig{
			{
				ID:           "github",
				Type:         "github",
				ClientID:     "gh-client-id",
				ClientSecret: "gh-client-secret",
				Scopes:       []string{"repo", "user"},
			},
			{
				ID:           "gitlab",
				Type:         "gitlab",
				ClientID:     "gl-client-id",
				ClientSecret: "gl-client-secret",
			},
		}

		result, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, envProviders)
		require.NoError(t, err)
		require.Len(t, result, 2)

		// Verify round-trip: the loaded configs should match input.
		byID := map[string]codersdk.ExternalAuthConfig{}
		for _, cfg := range result {
			byID[cfg.ID] = cfg
		}
		assert.Equal(t, "gh-client-id", byID["github"].ClientID)
		assert.Equal(t, "gh-client-secret", byID["github"].ClientSecret)
		assert.Equal(t, []string{"repo", "user"}, byID["github"].Scopes)
		assert.Equal(t, "gl-client-id", byID["gitlab"].ClientID)
	})

	t.Run("DeletesStaleEnvRows", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		logger := slogtest.Make(t, nil)
		ctx := testutil.Context(t, testutil.WaitShort)
		//nolint:gocritic // Unit test needs system context for dbauthz.
		ctx = dbauthz.AsSystemRestricted(ctx)

		// First sync with two providers.
		initial := []codersdk.ExternalAuthConfig{
			{ID: "github", Type: "github", ClientID: "gh-id", ClientSecret: "gh-secret"},
			{ID: "gitlab", Type: "gitlab", ClientID: "gl-id", ClientSecret: "gl-secret"},
		}
		_, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, initial)
		require.NoError(t, err)

		// Second sync removes gitlab from env config.
		reduced := []codersdk.ExternalAuthConfig{
			{ID: "github", Type: "github", ClientID: "gh-id", ClientSecret: "gh-secret"},
		}
		result, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, reduced)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "github", result[0].ID)
	})

	t.Run("EmptyEnvCleansAllEnvRows", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		logger := slogtest.Make(t, nil)
		ctx := testutil.Context(t, testutil.WaitShort)
		//nolint:gocritic // Unit test needs system context for dbauthz.
		ctx = dbauthz.AsSystemRestricted(ctx)

		// Sync with a provider first.
		initial := []codersdk.ExternalAuthConfig{
			{ID: "github", Type: "github", ClientID: "id", ClientSecret: "secret"},
		}
		_, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, initial)
		require.NoError(t, err)

		// Sync with empty list should remove all env rows.
		result, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, nil)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("PreservesDBSourcedProviders", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		logger := slogtest.Make(t, nil)
		ctx := testutil.Context(t, testutil.WaitShort)
		//nolint:gocritic // Unit test needs system context for dbauthz.
		ctx = dbauthz.AsSystemRestricted(ctx)

		// Insert a DB-sourced provider directly.
		_, err := db.InsertExternalAuthProviderConfig(ctx, database.InsertExternalAuthProviderConfigParams{
			ID:                    uuid.New(),
			CreatedAt:             dbtestutil.NowInDefaultTimezone(),
			UpdatedAt:             dbtestutil.NowInDefaultTimezone(),
			ProviderID:            "bitbucket",
			Type:                  "bitbucket-cloud",
			ClientID:              "bb-id",
			ClientSecretEncrypted: "bb-secret",
			Source:                "database",
			Scopes:                []string{},
			ExtraTokenKeys:        []string{},
			CodeChallengeMethods:  []string{},
		})
		require.NoError(t, err)

		// Sync env providers — the DB-sourced one should be preserved.
		envProviders := []codersdk.ExternalAuthConfig{
			{ID: "github", Type: "github", ClientID: "gh-id", ClientSecret: "gh-secret"},
		}
		result, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, envProviders)
		require.NoError(t, err)
		require.Len(t, result, 2)

		byID := map[string]codersdk.ExternalAuthConfig{}
		for _, cfg := range result {
			byID[cfg.ID] = cfg
		}
		assert.Contains(t, byID, "github")
		assert.Contains(t, byID, "bitbucket")
	})

	t.Run("UpsertUpdatesExistingProvider", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		logger := slogtest.Make(t, nil)
		ctx := testutil.Context(t, testutil.WaitShort)
		//nolint:gocritic // Unit test needs system context for dbauthz.
		ctx = dbauthz.AsSystemRestricted(ctx)

		// First sync.
		_, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, []codersdk.ExternalAuthConfig{
			{ID: "github", Type: "github", ClientID: "old-id", ClientSecret: "old-secret"},
		})
		require.NoError(t, err)

		// Second sync with updated client ID.
		result, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, []codersdk.ExternalAuthConfig{
			{ID: "github", Type: "github", ClientID: "new-id", ClientSecret: "new-secret"},
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "new-id", result[0].ClientID)
		assert.Equal(t, "new-secret", result[0].ClientSecret)
	})

	t.Run("ProviderIDFallsBackToType", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		logger := slogtest.Make(t, nil)
		ctx := testutil.Context(t, testutil.WaitShort)
		//nolint:gocritic // Unit test needs system context for dbauthz.
		ctx = dbauthz.AsSystemRestricted(ctx)

		// Provider with no ID should use Type as the provider ID.
		result, err := cli.SyncAndLoadExternalAuthProviders(ctx, logger, db, []codersdk.ExternalAuthConfig{
			{Type: "github", ClientID: "id", ClientSecret: "secret"},
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "github", result[0].ID)
	})
}
