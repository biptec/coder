package coderd

import (
	"net/http"

	"github.com/coder/coder/v2/coderd/externalauthproviders"
)

// @Summary List external auth provider configurations.
// @ID list-external-auth-provider-configurations
// @Security CoderSessionToken
// @Produce json
// @Tags ExternalAuthProviders
// @Success 200 {array} codersdk.ExternalAuthProviderEntry
// @Router /external-auth-providers [get]
func (api *API) externalAuthProviders() http.HandlerFunc {
	return externalauthproviders.List(api.Database)
}

// @Summary Get external auth provider configuration.
// @ID get-external-auth-provider-configuration
// @Security CoderSessionToken
// @Produce json
// @Tags ExternalAuthProviders
// @Param externalAuthProvider path string true "External Auth Provider ID" format(uuid)
// @Success 200 {object} codersdk.ExternalAuthProviderEntry
// @Router /external-auth-providers/{externalAuthProvider} [get]
func (api *API) externalAuthProvider() http.HandlerFunc {
	return externalauthproviders.Get(api.Database)
}

// @Summary Create external auth provider configuration.
// @ID create-external-auth-provider-configuration
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags ExternalAuthProviders
// @Param request body codersdk.CreateExternalAuthProviderRequest true "External auth provider configuration"
// @Success 201 {object} codersdk.ExternalAuthProviderEntry
// @Router /external-auth-providers [post]
func (api *API) postExternalAuthProvider() http.HandlerFunc {
	return externalauthproviders.Create(api.Database, api.ExternalAuthRegistry, api.Auditor.Load(), api.Logger)
}

// @Summary Update external auth provider configuration.
// @ID update-external-auth-provider-configuration
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags ExternalAuthProviders
// @Param externalAuthProvider path string true "External Auth Provider ID" format(uuid)
// @Param request body codersdk.UpdateExternalAuthProviderRequest true "Updated external auth provider configuration"
// @Success 200 {object} codersdk.ExternalAuthProviderEntry
// @Router /external-auth-providers/{externalAuthProvider} [put]
func (api *API) putExternalAuthProvider() http.HandlerFunc {
	return externalauthproviders.Update(api.Database, api.ExternalAuthRegistry, api.Auditor.Load(), api.Logger)
}

// @Summary Delete external auth provider configuration.
// @ID delete-external-auth-provider-configuration
// @Security CoderSessionToken
// @Tags ExternalAuthProviders
// @Param externalAuthProvider path string true "External Auth Provider ID" format(uuid)
// @Success 204
// @Router /external-auth-providers/{externalAuthProvider} [delete]
func (api *API) deleteExternalAuthProvider() http.HandlerFunc {
	return externalauthproviders.Delete(api.Database, api.ExternalAuthRegistry, api.Auditor.Load(), api.Logger)
}
