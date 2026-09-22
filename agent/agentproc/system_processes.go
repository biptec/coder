package agentproc

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	gopsprocess "github.com/shirou/gopsutil/v4/process"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// listSystemProcesses returns a best-effort point-in-time process snapshot.
// Individual processes can disappear while the snapshot is being collected,
// so per-process metadata errors are tolerated instead of failing the whole
// request.
func listSystemProcesses(ctx context.Context) ([]workspacesdk.SystemProcessInfo, error) {
	processes, err := gopsprocess.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	result := make([]workspacesdk.SystemProcessInfo, 0, len(processes))
	for _, process := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		info := workspacesdk.SystemProcessInfo{PID: process.Pid}
		if ppid, err := process.PpidWithContext(ctx); err == nil {
			info.PPID = ppid
		}
		if username, err := process.UsernameWithContext(ctx); err == nil {
			info.Username = username
		}
		if cpuPercent, err := process.CPUPercentWithContext(ctx); err == nil && !math.IsNaN(cpuPercent) && !math.IsInf(cpuPercent, 0) {
			info.CPUPercent = cpuPercent
		}
		if memoryPercent, err := process.MemoryPercentWithContext(ctx); err == nil && !math.IsNaN(float64(memoryPercent)) && !math.IsInf(float64(memoryPercent), 0) {
			info.MemoryPercent = memoryPercent
		}
		if createdAtMS, err := process.CreateTimeWithContext(ctx); err == nil && createdAtMS > 0 {
			info.StartedAtUnix = createdAtMS / 1000
			if info.StartedAtUnix <= now {
				info.ElapsedSeconds = now - info.StartedAtUnix
			}
		}
		if argv, err := process.CmdlineSliceWithContext(ctx); err == nil && len(argv) > 0 {
			info.Argv = argv
			info.Command = strings.Join(argv, " ")
		} else if name, err := process.NameWithContext(ctx); err == nil {
			info.Command = name
		}

		result = append(result, info)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].PID < result[j].PID
	})
	return result, nil
}
