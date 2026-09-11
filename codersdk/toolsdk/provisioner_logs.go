package toolsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk"
)

const (
	defaultProvisionerLogLimit = 200
	maxProvisionerLogLimit     = 1000
	provisionerLogPollInterval = 500 * time.Millisecond
)

// ProvisionerLogObservationResult is a bounded snapshot of provisioner logs.
// Complete is true only when the regular job API confirms a terminal state and
// the current cursor snapshot did not hit the response limit.
type ProvisionerLogObservationResult struct {
	Logs       []string                      `json:"logs"`
	NextCursor int64                         `json:"next_cursor"`
	HasMore    bool                          `json:"has_more"`
	Complete   bool                          `json:"complete"`
	JobStatus  codersdk.ProvisionerJobStatus `json:"job_status,omitempty"`
}

type provisionerLogSnapshotFetcher func(context.Context, int64) ([]codersdk.ProvisionerJobLog, error)

func fetchProvisionerLogSnapshot(ctx context.Context, client *codersdk.Client, path string, after int64) ([]codersdk.ProvisionerJobLog, error) {
	if after > 0 {
		path = fmt.Sprintf("%s?after=%d", path, after)
	}
	res, err := client.Request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, codersdk.ReadBodyAsError(res)
	}

	var logs []codersdk.ProvisionerJobLog
	if err := json.NewDecoder(res.Body).Decode(&logs); err != nil {
		return nil, xerrors.Errorf("decode provisioner logs: %w", err)
	}
	return logs, nil
}

func setProvisionerLogJobState(result *ProvisionerLogObservationResult, job codersdk.ProvisionerJob) {
	result.JobStatus = job.Status
	terminal := false
	switch job.Status {
	case codersdk.ProvisionerJobSucceeded, codersdk.ProvisionerJobFailed, codersdk.ProvisionerJobCanceled:
		terminal = true
	}
	// The non-follow snapshot is authoritative for all logs currently after the
	// cursor. Once the job is terminal, no future logs can appear. If the local
	// response limit was not hit, this cursor is fully drained.
	result.Complete = terminal && !result.HasMore
}

func observeProvisionerLogs(
	ctx context.Context,
	budget mcpObservationBudget,
	cursor int64,
	waitTimeoutMs *int,
	limit int,
	fetch provisionerLogSnapshotFetcher,
) (ProvisionerLogObservationResult, error) {
	if cursor < 0 {
		return ProvisionerLogObservationResult{}, xerrors.New("cursor cannot be negative")
	}
	wait, err := workspaceProcessWaitDuration(waitTimeoutMs)
	if err != nil {
		return ProvisionerLogObservationResult{}, err
	}
	if limit == 0 {
		limit = defaultProvisionerLogLimit
	}
	if limit < 1 || limit > maxProvisionerLogLimit {
		return ProvisionerLogObservationResult{}, xerrors.Errorf("limit must be between 1 and %d", maxProvisionerLogLimit)
	}

	result := ProvisionerLogObservationResult{
		Logs:       make([]string, 0, min(limit, defaultProvisionerLogLimit)),
		NextCursor: cursor,
	}

	// Preserve a small tail of the shared budget for the regular job-status API
	// lookup performed by the handler after this function returns.
	wait = workspaceProcessWaitWithinBudget(wait, budget)
	observationCtx, cancel := budget.context(ctx)
	defer cancel()

	deadline := time.Now().Add(wait)
	for {
		logs, err := fetch(observationCtx, cursor)
		if err != nil {
			return ProvisionerLogObservationResult{}, err
		}
		if len(logs) > 0 {
			if len(logs) > limit {
				result.HasMore = true
				logs = logs[:limit]
			}
			for _, log := range logs {
				result.Logs = append(result.Logs, log.Output)
				if log.ID > result.NextCursor {
					result.NextCursor = log.ID
				}
			}
			return result, nil
		}

		if wait <= 0 || !time.Now().Before(deadline) {
			return result, nil
		}

		delay := provisionerLogPollInterval
		if remaining := time.Until(deadline); remaining < delay {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-observationCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return ProvisionerLogObservationResult{}, ctx.Err()
			}
			return result, nil
		case <-timer.C:
		}
	}
}
