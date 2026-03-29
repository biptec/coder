package externalauth_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/externalauth"
)

func TestExternalAuthProviderConfigToSDK_AllFields(t *testing.T) {
	t.Parallel()

	dbCfg := database.ExternalAuthProviderConfig{
		ProviderID:            "github",
		Type:                  "github",
		ClientID:              "client-id-123",
		ClientSecretEncrypted: "decrypted-secret",
		DisplayName:           "GitHub",
		DisplayIcon:           "/icons/github.svg",
		AuthUrl:               "https://github.com/login/oauth/authorize",
		TokenUrl:              "https://github.com/login/oauth/access_token",
		ValidateUrl:           "https://api.github.com/user",
		RevokeUrl:             "https://api.github.com/revoke",
		DeviceCodeUrl:         "https://github.com/login/device/code",
		Scopes:                []string{"repo", "user"},
		ExtraTokenKeys:        []string{"authed_user"},
		NoRefresh:             true,
		DeviceFlow:            true,
		Regex:                 `github\.com`,
		AppInstallUrl:         "https://github.com/apps/my-app/installations/new",
		AppInstallationsUrl:   "https://api.github.com/app/installations",
		CodeChallengeMethods:  []string{"S256", "plain"},
	}

	sdk := externalauth.ExternalAuthProviderConfigToSDK(dbCfg)

	require.Equal(t, dbCfg.ProviderID, sdk.ID)
	require.Equal(t, dbCfg.Type, sdk.Type)
	require.Equal(t, dbCfg.ClientID, sdk.ClientID)
	require.Equal(t, dbCfg.ClientSecretEncrypted, sdk.ClientSecret)
	require.Equal(t, dbCfg.DisplayName, sdk.DisplayName)
	require.Equal(t, dbCfg.DisplayIcon, sdk.DisplayIcon)
	require.Equal(t, dbCfg.AuthUrl, sdk.AuthURL)
	require.Equal(t, dbCfg.TokenUrl, sdk.TokenURL)
	require.Equal(t, dbCfg.ValidateUrl, sdk.ValidateURL)
	require.Equal(t, dbCfg.RevokeUrl, sdk.RevokeURL)
	require.Equal(t, dbCfg.DeviceCodeUrl, sdk.DeviceCodeURL)
	require.Equal(t, dbCfg.Scopes, sdk.Scopes)
	require.Equal(t, dbCfg.ExtraTokenKeys, sdk.ExtraTokenKeys)
	require.Equal(t, dbCfg.NoRefresh, sdk.NoRefresh)
	require.Equal(t, dbCfg.DeviceFlow, sdk.DeviceFlow)
	require.Equal(t, dbCfg.Regex, sdk.Regex)
	require.Equal(t, dbCfg.AppInstallUrl, sdk.AppInstallURL)
	require.Equal(t, dbCfg.AppInstallationsUrl, sdk.AppInstallationsURL)
	require.Equal(t, dbCfg.CodeChallengeMethods, sdk.CodeChallengeMethodsSupported)
}

func TestExternalAuthProviderConfigToSDK_EmptyOptionalFields(t *testing.T) {
	t.Parallel()

	dbCfg := database.ExternalAuthProviderConfig{
		ProviderID:           "minimal",
		Type:                 "generic",
		ClientID:             "cid",
		Scopes:               []string{},
		ExtraTokenKeys:       []string{},
		CodeChallengeMethods: []string{},
	}

	sdk := externalauth.ExternalAuthProviderConfigToSDK(dbCfg)

	require.Equal(t, "minimal", sdk.ID)
	require.Equal(t, "generic", sdk.Type)
	require.Equal(t, "cid", sdk.ClientID)

	// Empty strings for optional URL fields.
	require.Empty(t, sdk.ClientSecret)
	require.Empty(t, sdk.DisplayName)
	require.Empty(t, sdk.DisplayIcon)
	require.Empty(t, sdk.AuthURL)
	require.Empty(t, sdk.TokenURL)
	require.Empty(t, sdk.ValidateURL)
	require.Empty(t, sdk.RevokeURL)
	require.Empty(t, sdk.DeviceCodeURL)
	require.Empty(t, sdk.Regex)
	require.Empty(t, sdk.AppInstallURL)
	require.Empty(t, sdk.AppInstallationsURL)

	// Empty slices should remain empty (not nil).
	require.Empty(t, sdk.Scopes)
	require.Empty(t, sdk.ExtraTokenKeys)
	require.Empty(t, sdk.CodeChallengeMethodsSupported)

	// Booleans default to false.
	require.False(t, sdk.NoRefresh)
	require.False(t, sdk.DeviceFlow)
}

func TestExternalAuthProviderConfigToSDK_NilSlices(t *testing.T) {
	t.Parallel()

	// Defensive: if the DB somehow returns nil slices, the
	// converter should not panic.
	dbCfg := database.ExternalAuthProviderConfig{
		ProviderID:           "nil-slices",
		Type:                 "generic",
		ClientID:             "cid",
		Scopes:               nil,
		ExtraTokenKeys:       nil,
		CodeChallengeMethods: nil,
	}

	// Should not panic.
	sdk := externalauth.ExternalAuthProviderConfigToSDK(dbCfg)

	require.Equal(t, "nil-slices", sdk.ID)
	// Nil slices pass through — they should not cause issues.
	require.Nil(t, sdk.Scopes)
	require.Nil(t, sdk.ExtraTokenKeys)
	require.Nil(t, sdk.CodeChallengeMethodsSupported)
}
