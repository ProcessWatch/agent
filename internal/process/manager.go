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

// Match returns every running process satisfying the selector.
//
// Unlike the old name-only Find, this fetches CPU, memory and start time only
// for processes that actually match, rather than for every process on the box
// just to filter them afterwards.
func (pm *ProcessManager) Match(ctx context.Context, sel core.Selector) ([]core.Process, error) {
	if sel.Mode == core.MatchUnit {
		return matchUnit(ctx, sel.Value)
	}

	procs, err := gopsprocess.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	needle := strings.ToLower(sel.Value)
	var matches []core.Process

	for _, p := range procs {
		name, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}

		var cmdline string
		if sel.Mode == core.MatchCmdline {
			// Only read the command line when the selector needs it — it is
			// an extra syscall per process, and sensitive.
			cmdline, err = p.CmdlineWithContext(ctx)
			if err != nil {
				continue
			}
		}

		if !selectorMatches(sel.Mode, needle, name, cmdline) {
			continue
		}

		proc := pidToProcess(ctx, p, name)
		proc.Cmdline = cmdline
		matches = append(matches, proc)
	}

	return matches, nil
}

func selectorMatches(mode core.MatchMode, needle, name, cmdline string) bool {
	switch mode {
	case core.MatchExact:
		return strings.EqualFold(name, needle)
	case core.MatchCmdline:
		return strings.Contains(strings.ToLower(cmdline), needle)
	default: // core.MatchSubstring
		return strings.Contains(strings.ToLower(name), needle)
	}
}

func pidToProcess(ctx context.Context, p *gopsprocess.Process, name string) core.Process {
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

	return core.Process{
		Name:          name,
		PID:           p.Pid,
		State:         "running",
		CPUPercent:    cpuPct,
		MemoryMB:      memMB,
		UptimeSeconds: uptimeSecs,
	}
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
