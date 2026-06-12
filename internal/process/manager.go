// Package process implements core.ProcessManager against the real OS:
// gopsutil for process inspection, the system shell for recovery commands.
package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ethan-mdev/process-watch/internal/core"
	gopsprocess "github.com/shirou/gopsutil/v4/process"
)

// pipeWaitDelay bounds how long Restart waits on inherited stdout/stderr
// pipes after the direct child exits — a detached grandchild holding the
// pipe open must not block the watcher.
const pipeWaitDelay = 5 * time.Second

type ProcessManager struct {
	restartTimeout time.Duration
}

func NewProcessManager(restartTimeout time.Duration) *ProcessManager {
	return &ProcessManager{restartTimeout: restartTimeout}
}

// ListAll returns a snapshot of all running OS processes.
func (pm *ProcessManager) ListAll(ctx context.Context) ([]core.Process, error) {
	procs, err := gopsprocess.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	results := make([]core.Process, 0, len(procs))
	for _, p := range procs {
		name, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}

		cpuPct, _ := p.CPUPercentWithContext(ctx)
		memInfo, _ := p.MemoryInfoWithContext(ctx)

		var memMB float64
		if memInfo != nil {
			memMB = float64(memInfo.RSS) / 1024 / 1024
		}

		var uptimeSecs int64
		if created, err := p.CreateTimeWithContext(ctx); err == nil {
			uptimeSecs = int64(time.Since(time.Unix(created/1000, 0)).Seconds())
		}

		results = append(results, core.Process{
			Name:          name,
			PID:           p.Pid,
			State:         "running",
			CPUPercent:    cpuPct,
			MemoryMB:      memMB,
			UptimeSeconds: uptimeSecs,
		})
	}
	return results, nil
}

// Find returns all running processes whose name contains the given string
// (case-insensitive substring match). The match is deliberately loose so
// hand-edited watchlist entries still work, but it means watching "node"
// also matches "node_exporter" — watch the most specific name available.
func (pm *ProcessManager) Find(ctx context.Context, name string) ([]core.Process, error) {
	all, err := pm.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	lower := strings.ToLower(name)
	var matches []core.Process
	for _, p := range all {
		if strings.Contains(strings.ToLower(p.Name), lower) {
			matches = append(matches, p)
		}
	}
	return matches, nil
}

// Restart executes restartCmd via the system shell.
// On Windows: cmd /c <restartCmd>
// On Linux/macOS: sh -c <restartCmd>
// The command is killed if it does not return within the configured timeout,
// so a hung recovery command cannot stall the poll loop.
func (pm *ProcessManager) Restart(ctx context.Context, restartCmd string) error {
	if pm.restartTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pm.restartTimeout)
		defer cancel()
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", restartCmd)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", restartCmd)
		cmd.SysProcAttr = detachAttr()
	}
	cmd.WaitDelay = pipeWaitDelay

	out, err := cmd.CombinedOutput()
	// ErrWaitDelay means the command itself exited successfully and only its
	// inherited pipes were left open (e.g. by a process it started) — success.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("restart command timed out after %s", pm.restartTimeout)
		}
		return fmt.Errorf("restart command failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
