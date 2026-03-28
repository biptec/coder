ALTER TYPE resource_type ADD VALUE IF NOT EXISTS 'external_auth_provider_config';

CREATE TABLE external_auth_provider_configs (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Identity
    provider_id                 TEXT NOT NULL UNIQUE,
    type                        TEXT NOT NULL DEFAULT '',
    display_name                TEXT NOT NULL DEFAULT '',
    display_icon                TEXT NOT NULL DEFAULT '',

    -- OAuth2 credentials
    client_id                   TEXT NOT NULL,
    client_secret_encrypted     TEXT NOT NULL DEFAULT '',
    client_secret_key_id        TEXT DEFAULT '' REFERENCES dbcrypt_keys(active_key_digest),

    -- OAuth2 endpoints (empty = use type defaults)
    auth_url                    TEXT NOT NULL DEFAULT '',
    token_url                   TEXT NOT NULL DEFAULT '',
    validate_url                TEXT NOT NULL DEFAULT '',
    revoke_url                  TEXT NOT NULL DEFAULT '',
    device_code_url             TEXT NOT NULL DEFAULT '',

    -- Behavior
    scopes                      TEXT[] NOT NULL DEFAULT '{}',
    extra_token_keys            TEXT[] NOT NULL DEFAULT '{}',
    no_refresh                  BOOLEAN NOT NULL DEFAULT FALSE,
    device_flow                 BOOLEAN NOT NULL DEFAULT FALSE,
    regex                       TEXT NOT NULL DEFAULT '',

    -- GitHub App specific
    app_install_url             TEXT NOT NULL DEFAULT '',
    app_installations_url       TEXT NOT NULL DEFAULT '',

    -- PKCE
    code_challenge_methods      TEXT[] NOT NULL DEFAULT '{S256}',

    -- Source tracking
    source                      TEXT NOT NULL DEFAULT 'database'
);
