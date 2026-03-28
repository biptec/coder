package externalauth

import (
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// ExternalAuthProviderConfigToSDK converts a database
// ExternalAuthProviderConfig to the SDK deployment config type used
// by ConvertConfig. The dbcrypt layer handles decryption of
// ClientSecretEncrypted before this function is called.
func ExternalAuthProviderConfigToSDK(
	cfg database.ExternalAuthProviderConfig,
) codersdk.ExternalAuthConfig {
	return codersdk.ExternalAuthConfig{
		ID:                            cfg.ProviderID,
		Type:                          cfg.Type,
		ClientID:                      cfg.ClientID,
		ClientSecret:                  cfg.ClientSecretEncrypted,
		DisplayName:                   cfg.DisplayName,
		DisplayIcon:                   cfg.DisplayIcon,
		AuthURL:                       cfg.AuthUrl,
		TokenURL:                      cfg.TokenUrl,
		ValidateURL:                   cfg.ValidateUrl,
		RevokeURL:                     cfg.RevokeUrl,
		DeviceCodeURL:                 cfg.DeviceCodeUrl,
		Scopes:                        cfg.Scopes,
		ExtraTokenKeys:                cfg.ExtraTokenKeys,
		NoRefresh:                     cfg.NoRefresh,
		DeviceFlow:                    cfg.DeviceFlow,
		Regex:                         cfg.Regex,
		AppInstallURL:                 cfg.AppInstallUrl,
		AppInstallationsURL:           cfg.AppInstallationsUrl,
		CodeChallengeMethodsSupported: cfg.CodeChallengeMethods,
	}
}
