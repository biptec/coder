-- name: GetExternalAuthProviderConfigs :many
SELECT * FROM external_auth_provider_configs ORDER BY (display_name, provider_id) ASC;

-- name: GetExternalAuthProviderConfigByID :one
SELECT * FROM external_auth_provider_configs WHERE id = $1;

-- name: GetExternalAuthProviderConfigByProviderID :one
SELECT * FROM external_auth_provider_configs WHERE provider_id = $1;

-- name: InsertExternalAuthProviderConfig :one
INSERT INTO external_auth_provider_configs (
    id, created_at, updated_at,
    provider_id, type, display_name, display_icon,
    client_id, client_secret_encrypted, client_secret_key_id,
    auth_url, token_url, validate_url, revoke_url, device_code_url,
    scopes, extra_token_keys, no_refresh, device_flow, regex,
    app_install_url, app_installations_url,
    code_challenge_methods,
    source
) VALUES (
    $1, $2, $3,
    $4, $5, $6, $7,
    $8, $9, $10,
    $11, $12, $13, $14, $15,
    $16, $17, $18, $19, $20,
    $21, $22,
    $23,
    $24
) RETURNING *;

-- name: UpdateExternalAuthProviderConfig :one
UPDATE external_auth_provider_configs SET
    updated_at = $2,
    type = $3,
    display_name = $4,
    display_icon = $5,
    client_id = $6,
    client_secret_encrypted = $7,
    client_secret_key_id = $8,
    auth_url = $9,
    token_url = $10,
    validate_url = $11,
    revoke_url = $12,
    device_code_url = $13,
    scopes = $14,
    extra_token_keys = $15,
    no_refresh = $16,
    device_flow = $17,
    regex = $18,
    app_install_url = $19,
    app_installations_url = $20,
    code_challenge_methods = $21,
    source = $22
WHERE id = $1 RETURNING *;

-- name: DeleteExternalAuthProviderConfig :exec
DELETE FROM external_auth_provider_configs WHERE id = $1;

-- name: UpsertExternalAuthProviderConfigFromEnv :one
INSERT INTO external_auth_provider_configs (
    id, created_at, updated_at,
    provider_id, type, display_name, display_icon,
    client_id, client_secret_encrypted, client_secret_key_id,
    auth_url, token_url, validate_url, revoke_url, device_code_url,
    scopes, extra_token_keys, no_refresh, device_flow, regex,
    app_install_url, app_installations_url,
    code_challenge_methods,
    source
) VALUES (
    $1, $2, $3,
    $4, $5, $6, $7,
    $8, $9, $10,
    $11, $12, $13, $14, $15,
    $16, $17, $18, $19, $20,
    $21, $22,
    $23,
    'env'
) ON CONFLICT (provider_id) DO UPDATE SET
    updated_at = EXCLUDED.updated_at,
    type = EXCLUDED.type,
    display_name = EXCLUDED.display_name,
    display_icon = EXCLUDED.display_icon,
    client_id = EXCLUDED.client_id,
    client_secret_encrypted = EXCLUDED.client_secret_encrypted,
    client_secret_key_id = EXCLUDED.client_secret_key_id,
    auth_url = EXCLUDED.auth_url,
    token_url = EXCLUDED.token_url,
    validate_url = EXCLUDED.validate_url,
    revoke_url = EXCLUDED.revoke_url,
    device_code_url = EXCLUDED.device_code_url,
    scopes = EXCLUDED.scopes,
    extra_token_keys = EXCLUDED.extra_token_keys,
    no_refresh = EXCLUDED.no_refresh,
    device_flow = EXCLUDED.device_flow,
    regex = EXCLUDED.regex,
    app_install_url = EXCLUDED.app_install_url,
    app_installations_url = EXCLUDED.app_installations_url,
    code_challenge_methods = EXCLUDED.code_challenge_methods,
    source = 'env'
RETURNING *;

-- name: DeleteExternalAuthProviderConfigsBySourceNotInProviderIDs :exec
-- Removes env-sourced external auth provider configs whose provider_id
-- is not in the given list of active provider IDs. This is used during
-- startup to clean up stale env-sourced rows that are no longer present
-- in the deployment configuration.
DELETE FROM external_auth_provider_configs
WHERE source = 'env'
  AND provider_id != ALL(@active_provider_ids::text[]);
