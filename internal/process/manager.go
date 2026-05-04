package process

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ethan-mdev/process-watch/internal/core"
	gopsprocess "github.com/shirou/gopsutil/v4/process"
)

// shellMetachars are characters that warrant a warning when found in restartCmd.
var shellMetachars = []string{"|", ";", "&&", "`"}

type ProcessManager struct{}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{}
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
// (case-insensitive substring match).
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

// IsRunning returns true if at least one process matching name is running.
func (pm *ProcessManager) IsRunning(ctx context.Context, name string) (bool, error) {
	matches, err := pm.Find(ctx, name)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// Restart executes restartCmd via the system shell.
// On Windows: cmd /c <restartCmd>
// On Linux/macOS: sh -c <restartCmd>
func (pm *ProcessManager) Restart(ctx context.Context, restartCmd string) error {
	for _, meta := range shellMetachars {
		if strings.Contains(restartCmd, meta) {
			log.Printf("[WARN] restartCmd contains shell metacharacter %q — ensure this is trusted input", meta)
		}
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", restartCmd)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", restartCmd)
		cmd.SysProcAttr = detachAttr()
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart command failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
