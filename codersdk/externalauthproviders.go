package codersdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ExternalAuthProviderEntry represents a configured external auth provider.
type ExternalAuthProviderEntry struct {
	ID                   uuid.UUID `json:"id" format:"uuid"`
	ProviderID           string    `json:"provider_id"`
	Type                 string    `json:"type"`
	DisplayName          string    `json:"display_name"`
	DisplayIcon          string    `json:"display_icon"`
	ClientID             string    `json:"client_id"`
	HasClientSecret      bool      `json:"has_client_secret"`
	AuthURL              string    `json:"auth_url"`
	TokenURL             string    `json:"token_url"`
	ValidateURL          string    `json:"validate_url"`
	RevokeURL            string    `json:"revoke_url"`
	DeviceCodeURL        string    `json:"device_code_url"`
	Scopes               []string  `json:"scopes"`
	ExtraTokenKeys       []string  `json:"extra_token_keys"`
	NoRefresh            bool      `json:"no_refresh"`
	DeviceFlow           bool      `json:"device_flow"`
	Regex                string    `json:"regex"`
	AppInstallURL        string    `json:"app_install_url"`
	AppInstallationsURL  string    `json:"app_installations_url"`
	CodeChallengeMethods []string  `json:"code_challenge_methods"`
	Source               string    `json:"source"`
	CreatedAt            time.Time `json:"created_at" format:"date-time"`
	UpdatedAt            time.Time `json:"updated_at" format:"date-time"`
}

// CreateExternalAuthProviderRequest is the request body for creating a new
// external auth provider configuration.
type CreateExternalAuthProviderRequest struct {
	ProviderID           string   `json:"provider_id" validate:"required"`
	Type                 string   `json:"type" validate:"required"`
	DisplayName          string   `json:"display_name"`
	DisplayIcon          string   `json:"display_icon"`
	ClientID             string   `json:"client_id" validate:"required"`
	ClientSecret         string   `json:"client_secret"`
	AuthURL              string   `json:"auth_url"`
	TokenURL             string   `json:"token_url"`
	ValidateURL          string   `json:"validate_url"`
	RevokeURL            string   `json:"revoke_url"`
	DeviceCodeURL        string   `json:"device_code_url"`
	Scopes               []string `json:"scopes"`
	ExtraTokenKeys       []string `json:"extra_token_keys"`
	NoRefresh            bool     `json:"no_refresh"`
	DeviceFlow           bool     `json:"device_flow"`
	Regex                string   `json:"regex"`
	AppInstallURL        string   `json:"app_install_url"`
	AppInstallationsURL  string   `json:"app_installations_url"`
	CodeChallengeMethods []string `json:"code_challenge_methods"`
}

// UpdateExternalAuthProviderRequest is the request body for updating an
// existing external auth provider configuration. This is a full replace
// (PUT semantics).
type UpdateExternalAuthProviderRequest struct {
	Type        string `json:"type" validate:"required"`
	DisplayName string `json:"display_name"`
	DisplayIcon string `json:"display_icon"`
	ClientID    string `json:"client_id" validate:"required"`
	// ClientSecret is optional. If nil, the existing secret is preserved.
	// If non-nil, the secret is replaced with the new value.
	ClientSecret         *string  `json:"client_secret,omitempty"`
	AuthURL              string   `json:"auth_url"`
	TokenURL             string   `json:"token_url"`
	ValidateURL          string   `json:"validate_url"`
	RevokeURL            string   `json:"revoke_url"`
	DeviceCodeURL        string   `json:"device_code_url"`
	Scopes               []string `json:"scopes"`
	ExtraTokenKeys       []string `json:"extra_token_keys"`
	NoRefresh            bool     `json:"no_refresh"`
	DeviceFlow           bool     `json:"device_flow"`
	Regex                string   `json:"regex"`
	AppInstallURL        string   `json:"app_install_url"`
	AppInstallationsURL  string   `json:"app_installations_url"`
	CodeChallengeMethods []string `json:"code_challenge_methods"`
}

// ExternalAuthProviders returns all configured external auth providers.
func (c *Client) ExternalAuthProviders(ctx context.Context) ([]ExternalAuthProviderEntry, error) {
	res, err := c.Request(ctx, http.MethodGet, "/api/v2/external-auth-providers", nil)
	if err != nil {
		return []ExternalAuthProviderEntry{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return []ExternalAuthProviderEntry{}, ReadBodyAsError(res)
	}
	var entries []ExternalAuthProviderEntry
	return entries, json.NewDecoder(res.Body).Decode(&entries)
}

// ExternalAuthProvider returns a single external auth provider by ID.
func (c *Client) ExternalAuthProvider(ctx context.Context, id uuid.UUID) (ExternalAuthProviderEntry, error) {
	res, err := c.Request(ctx, http.MethodGet, fmt.Sprintf("/api/v2/external-auth-providers/%s", id), nil)
	if err != nil {
		return ExternalAuthProviderEntry{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ExternalAuthProviderEntry{}, ReadBodyAsError(res)
	}
	var entry ExternalAuthProviderEntry
	return entry, json.NewDecoder(res.Body).Decode(&entry)
}

// CreateExternalAuthProvider creates a new external auth provider
// configuration.
func (c *Client) CreateExternalAuthProvider(ctx context.Context, req CreateExternalAuthProviderRequest) (ExternalAuthProviderEntry, error) {
	res, err := c.Request(ctx, http.MethodPost, "/api/v2/external-auth-providers", req)
	if err != nil {
		return ExternalAuthProviderEntry{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return ExternalAuthProviderEntry{}, ReadBodyAsError(res)
	}
	var entry ExternalAuthProviderEntry
	return entry, json.NewDecoder(res.Body).Decode(&entry)
}

// UpdateExternalAuthProvider updates an existing external auth provider
// configuration.
func (c *Client) UpdateExternalAuthProvider(ctx context.Context, id uuid.UUID, req UpdateExternalAuthProviderRequest) (ExternalAuthProviderEntry, error) {
	res, err := c.Request(ctx, http.MethodPut, fmt.Sprintf("/api/v2/external-auth-providers/%s", id), req)
	if err != nil {
		return ExternalAuthProviderEntry{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ExternalAuthProviderEntry{}, ReadBodyAsError(res)
	}
	var entry ExternalAuthProviderEntry
	return entry, json.NewDecoder(res.Body).Decode(&entry)
}

// DeleteExternalAuthProvider deletes an external auth provider configuration.
func (c *Client) DeleteExternalAuthProvider(ctx context.Context, id uuid.UUID) error {
	res, err := c.Request(ctx, http.MethodDelete, fmt.Sprintf("/api/v2/external-auth-providers/%s", id), nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return ReadBodyAsError(res)
	}
	return nil
}
