package externalauth

import (
	"context"
	"net/url"
	"sync"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/promoauth"
	"github.com/coder/coder/v2/codersdk"
)

// Registry provides thread-safe access to external auth provider
// configurations. It supports atomic replacement of the full set
// of providers via Replace and on-demand reload from the database
// via Reload.
type Registry struct {
	mu        sync.RWMutex
	reloadMu  sync.Mutex         // serializes Reload() calls
	providers map[string]*Config // keyed by Config.ID
	ordered   []*Config          // preserves insertion order for List()

	// Dependencies for Reload, set via SetReloadDeps after API
	// creation.
	logger     slog.Logger
	db         database.Store
	instrument *promoauth.Factory
	accessURL  *url.URL
}

// NewRegistry creates a Registry pre-populated with the given
// configs. Call SetReloadDeps before using Reload.
func NewRegistry(logger slog.Logger, configs []*Config) *Registry {
	r := &Registry{logger: logger}
	r.replace(configs)
	return r
}

// SetReloadDeps stores the dependencies needed by Reload. It must
// be called before the first Reload invocation (typically right
// after the API is created).
func (r *Registry) SetReloadDeps(db database.Store, instrument *promoauth.Factory, accessURL *url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.db = db
	r.instrument = instrument
	r.accessURL = accessURL
}

// Get returns the Config for the given provider ID, or false if
// it does not exist.
func (r *Registry) Get(providerID string) (*Config, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.providers[providerID]
	return cfg, ok
}

// List returns a copy of the ordered provider slice. Callers may
// iterate the returned slice without holding any lock.
func (r *Registry) List() []*Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Config, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// Len returns the number of registered providers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.ordered)
}

// Replace atomically swaps the full set of providers.
func (r *Registry) Replace(configs []*Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replace(configs)
}

// replace rebuilds the internal map and ordered slice. Caller
// must hold r.mu for writing.
func (r *Registry) replace(configs []*Config) {
	m := make(map[string]*Config, len(configs))
	ordered := make([]*Config, len(configs))
	for i, c := range configs {
		m[c.ID] = c
		ordered[i] = c
	}
	r.providers = m
	r.ordered = ordered
}

// Reload reads all external auth provider configurations from the
// database, converts them to *Config via ConvertConfig, and
// atomically replaces the registry contents. SetReloadDeps must
// have been called first.
func (r *Registry) Reload(ctx context.Context) error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	r.mu.RLock()
	db := r.db
	instrument := r.instrument
	accessURL := r.accessURL
	r.mu.RUnlock()

	if db == nil || instrument == nil || accessURL == nil {
		return xerrors.New("registry reload deps not set; call SetReloadDeps first")
	}

	// Use system-restricted context for the DB query since
	// reloads may be triggered outside a user request context.
	//nolint:gocritic // Reload needs system privileges to read all provider configs.
	rows, err := db.GetExternalAuthProviderConfigs(dbauthz.AsSystemRestricted(ctx))
	if err != nil {
		return xerrors.Errorf("get external auth provider configs: %w", err)
	}

	sdkConfigs := make([]codersdk.ExternalAuthConfig, 0, len(rows))
	for _, row := range rows {
		sdkConfigs = append(sdkConfigs, ExternalAuthProviderConfigToSDK(row))
	}

	var configs []*Config
	for _, sdkCfg := range sdkConfigs {
		cfgs, err := ConvertConfig(instrument, []codersdk.ExternalAuthConfig{sdkCfg}, accessURL)
		if err != nil {
			r.logger.Warn(ctx, "skipping invalid external auth provider during reload",
				slog.F("provider_id", sdkCfg.ID),
				slog.Error(err))
			continue
		}
		configs = append(configs, cfgs...)
	}

	r.Replace(configs)
	r.logger.Info(ctx, "reloaded external auth provider registry",
		slog.F("count", len(configs)),
	)
	return nil
}
