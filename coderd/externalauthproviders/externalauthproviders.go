package externalauthproviders

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/audit"
	"github.com/coder/coder/v2/coderd/externalauth"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/codersdk"
)

// List returns an http.HandlerFunc that handles
// GET /external-auth-providers.
func List(db database.Store) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		configs, err := db.GetExternalAuthProviderConfigs(ctx)
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}

		entries := make([]codersdk.ExternalAuthProviderEntry, 0, len(configs))
		for _, cfg := range configs {
			entries = append(entries, convertProviderConfig(cfg))
		}
		httpapi.Write(ctx, rw, http.StatusOK, entries)
	}
}

// Get returns an http.HandlerFunc that handles
// GET /external-auth-providers/{externalAuthProvider}.
func Get(db database.Store) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		id, ok := httpmw.ParseUUIDParam(rw, r, "externalAuthProvider")
		if !ok {
			return
		}

		cfg, err := db.GetExternalAuthProviderConfigByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
				Message: "External auth provider configuration not found.",
			})
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}

		httpapi.Write(ctx, rw, http.StatusOK, convertProviderConfig(cfg))
	}
}

// Create returns an http.HandlerFunc that handles
// POST /external-auth-providers.
func Create(db database.Store, registry *externalauth.Registry, auditor *audit.Auditor, logger slog.Logger) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var (
			ctx               = r.Context()
			aReq, commitAudit = audit.InitRequest[database.ExternalAuthProviderConfig](rw, &audit.RequestParams{
				Audit:   *auditor,
				Log:     logger,
				Request: r,
				Action:  database.AuditActionCreate,
			})
		)
		defer commitAudit()

		var req codersdk.CreateExternalAuthProviderRequest
		if !httpapi.Read(ctx, rw, r, &req) {
			return
		}

		cfg, err := db.InsertExternalAuthProviderConfig(ctx, database.InsertExternalAuthProviderConfigParams{
			ID:                    uuid.New(),
			CreatedAt:             dbtime.Now(),
			UpdatedAt:             dbtime.Now(),
			ProviderID:            req.ProviderID,
			Type:                  req.Type,
			DisplayName:           req.DisplayName,
			DisplayIcon:           req.DisplayIcon,
			ClientID:              req.ClientID,
			ClientSecretEncrypted: req.ClientSecret,
			ClientSecretKeyID:     sql.NullString{},
			AuthUrl:               req.AuthURL,
			TokenUrl:              req.TokenURL,
			ValidateUrl:           req.ValidateURL,
			RevokeUrl:             req.RevokeURL,
			DeviceCodeUrl:         req.DeviceCodeURL,
			Scopes:                req.Scopes,
			ExtraTokenKeys:        req.ExtraTokenKeys,
			NoRefresh:             req.NoRefresh,
			DeviceFlow:            req.DeviceFlow,
			Regex:                 req.Regex,
			AppInstallUrl:         req.AppInstallURL,
			AppInstallationsUrl:   req.AppInstallationsURL,
			CodeChallengeMethods:  req.CodeChallengeMethods,
			Source:                "database",
		})
		if err != nil {
			httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
				Message: "Internal error creating external auth provider configuration.",
				Detail:  err.Error(),
			})
			return
		}

		if err := registry.Reload(ctx); err != nil {
			logger.Error(ctx, "failed to reload external auth registry", slog.Error(err))
		}

		aReq.New = cfg
		httpapi.Write(ctx, rw, http.StatusCreated, convertProviderConfig(cfg))
	}
}

// Update returns an http.HandlerFunc that handles
// PUT /external-auth-providers/{externalAuthProvider}.
func Update(db database.Store, registry *externalauth.Registry, auditor *audit.Auditor, logger slog.Logger) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var (
			ctx               = r.Context()
			aReq, commitAudit = audit.InitRequest[database.ExternalAuthProviderConfig](rw, &audit.RequestParams{
				Audit:   *auditor,
				Log:     logger,
				Request: r,
				Action:  database.AuditActionWrite,
			})
		)
		defer commitAudit()

		id, ok := httpmw.ParseUUIDParam(rw, r, "externalAuthProvider")
		if !ok {
			return
		}

		existing, err := db.GetExternalAuthProviderConfigByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
				Message: "External auth provider configuration not found.",
			})
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}

		if existing.Source == "env" {
			httpapi.Write(ctx, rw, http.StatusForbidden, codersdk.Response{
				Message: "Cannot modify environment-sourced external auth provider configurations.",
			})
			return
		}

		aReq.Old = existing

		var req codersdk.UpdateExternalAuthProviderRequest
		if !httpapi.Read(ctx, rw, r, &req) {
			return
		}

		params := database.UpdateExternalAuthProviderConfigParams{
			ID:                    id,
			UpdatedAt:             dbtime.Now(),
			Type:                  req.Type,
			DisplayName:           req.DisplayName,
			DisplayIcon:           req.DisplayIcon,
			ClientID:              req.ClientID,
			ClientSecretEncrypted: existing.ClientSecretEncrypted,
			ClientSecretKeyID:     existing.ClientSecretKeyID,
			AuthUrl:               req.AuthURL,
			TokenUrl:              req.TokenURL,
			ValidateUrl:           req.ValidateURL,
			RevokeUrl:             req.RevokeURL,
			DeviceCodeUrl:         req.DeviceCodeURL,
			Scopes:                req.Scopes,
			ExtraTokenKeys:        req.ExtraTokenKeys,
			NoRefresh:             req.NoRefresh,
			DeviceFlow:            req.DeviceFlow,
			Regex:                 req.Regex,
			AppInstallUrl:         req.AppInstallURL,
			AppInstallationsUrl:   req.AppInstallationsURL,
			CodeChallengeMethods:  req.CodeChallengeMethods,
			Source:                existing.Source,
		}
		if req.ClientSecret != nil {
			params.ClientSecretEncrypted = *req.ClientSecret
			// Reset key ID so dbcrypt re-encrypts with the current
			// active key on the next encryption pass.
			params.ClientSecretKeyID = sql.NullString{}
		}

		cfg, err := db.UpdateExternalAuthProviderConfig(ctx, params)
		if err != nil {
			httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
				Message: "Internal error updating external auth provider configuration.",
				Detail:  err.Error(),
			})
			return
		}

		if err := registry.Reload(ctx); err != nil {
			logger.Error(ctx, "failed to reload external auth registry", slog.Error(err))
		}

		aReq.New = cfg
		httpapi.Write(ctx, rw, http.StatusOK, convertProviderConfig(cfg))
	}
}

// Delete returns an http.HandlerFunc that handles
// DELETE /external-auth-providers/{externalAuthProvider}.
func Delete(db database.Store, registry *externalauth.Registry, auditor *audit.Auditor, logger slog.Logger) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var (
			ctx               = r.Context()
			aReq, commitAudit = audit.InitRequest[database.ExternalAuthProviderConfig](rw, &audit.RequestParams{
				Audit:   *auditor,
				Log:     logger,
				Request: r,
				Action:  database.AuditActionDelete,
			})
		)
		defer commitAudit()

		id, ok := httpmw.ParseUUIDParam(rw, r, "externalAuthProvider")
		if !ok {
			return
		}

		existing, err := db.GetExternalAuthProviderConfigByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.Write(ctx, rw, http.StatusNotFound, codersdk.Response{
				Message: "External auth provider configuration not found.",
			})
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}

		if existing.Source == "env" {
			httpapi.Write(ctx, rw, http.StatusForbidden, codersdk.Response{
				Message: "Cannot modify environment-sourced external auth provider configurations.",
			})
			return
		}

		aReq.Old = existing

		err = db.DeleteExternalAuthProviderConfig(ctx, id)
		if err != nil {
			httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
				Message: "Internal error deleting external auth provider configuration.",
				Detail:  err.Error(),
			})
			return
		}

		if err := registry.Reload(ctx); err != nil {
			logger.Error(ctx, "failed to reload external auth registry", slog.Error(err))
		}

		rw.WriteHeader(http.StatusNoContent)
	}
}

func convertProviderConfig(cfg database.ExternalAuthProviderConfig) codersdk.ExternalAuthProviderEntry {
	return codersdk.ExternalAuthProviderEntry{
		ID:                   cfg.ID,
		ProviderID:           cfg.ProviderID,
		Type:                 cfg.Type,
		DisplayName:          cfg.DisplayName,
		DisplayIcon:          cfg.DisplayIcon,
		ClientID:             cfg.ClientID,
		HasClientSecret:      cfg.ClientSecretEncrypted != "",
		AuthURL:              cfg.AuthUrl,
		TokenURL:             cfg.TokenUrl,
		ValidateURL:          cfg.ValidateUrl,
		RevokeURL:            cfg.RevokeUrl,
		DeviceCodeURL:        cfg.DeviceCodeUrl,
		Scopes:               cfg.Scopes,
		ExtraTokenKeys:       cfg.ExtraTokenKeys,
		NoRefresh:            cfg.NoRefresh,
		DeviceFlow:           cfg.DeviceFlow,
		Regex:                cfg.Regex,
		AppInstallURL:        cfg.AppInstallUrl,
		AppInstallationsURL:  cfg.AppInstallationsUrl,
		CodeChallengeMethods: cfg.CodeChallengeMethods,
		Source:               cfg.Source,
		CreatedAt:            cfg.CreatedAt,
		UpdatedAt:            cfg.UpdatedAt,
	}
}
