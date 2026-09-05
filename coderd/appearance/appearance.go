package appearance

import (
	"context"
	"time"

	"github.com/coder/coder/v2/codersdk"
)

type Fetcher interface {
	Fetch(ctx context.Context) (codersdk.AppearanceConfig, error)
}

type AGPLFetcher struct {
	docsURL                       string
	workspaceActivityNowThreshold time.Duration
}

func (f AGPLFetcher) Fetch(context.Context) (codersdk.AppearanceConfig, error) {
	return codersdk.AppearanceConfig{
		AnnouncementBanners:             []codersdk.BannerConfig{},
		SupportLinks:                    codersdk.DefaultSupportLinks(f.docsURL),
		DocsURL:                         f.docsURL,
		WorkspaceActivityNowThresholdMS: f.workspaceActivityNowThreshold.Milliseconds(),
	}, nil
}

func NewDefaultFetcher(docsURL string, workspaceActivityNowThreshold time.Duration) Fetcher {
	if docsURL == "" {
		docsURL = codersdk.DefaultDocsURL()
	}
	return &AGPLFetcher{
		docsURL:                       docsURL,
		workspaceActivityNowThreshold: workspaceActivityNowThreshold,
	}
}
