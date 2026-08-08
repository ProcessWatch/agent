//go:build linux

package process

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ethan-mdev/process-watch/internal/core"
	gopsprocess "github.com/shirou/gopsutil/v4/process"
)

// unitQueryTimeout bounds the systemctl calls so a wedged systemd cannot stall
// the poll loop.
const unitQueryTimeout = 5 * time.Second

// matchUnit asks systemd which units match a pattern and are actually running.
//
// The pattern may be a plain unit name ("postgresql.service") or a glob
// ("gt-web@*.service"), which is how a templated worker group is watched as a
// single entry. Only units that are both active and running are returned, so a
// unit sitting in failed or activating does not read as healthy.
//
// This asks the service manager what it believes rather than inferring state
// from the process table, which is why it is the most reliable mode available.
func matchUnit(ctx context.Context, pattern string) ([]core.Process, error) {
	if pattern == "" {
		return nil, fmt.Errorf("unit selector is empty")
	}
	if strings.ContainsAny(pattern, " \t\n") {
		return nil, fmt.Errorf("unit selector %q contains whitespace", pattern)
	}
	if !strings.Contains(pattern, ".") {
		pattern += ".service"
	}

	names, err := listUnits(ctx, pattern)
	if err != nil {
		return nil, err
	}

	var matches []core.Process
	for _, unit := range names {
		props, err := showUnit(ctx, unit)
		if err != nil {
			continue
		}
		if props["ActiveState"] != "active" || props["SubState"] != "running" {
			continue
		}

		pid, _ := strconv.Atoi(props["MainPID"])
		proc := core.Process{
			Name:  unit,
			PID:   int32(pid),
			State: "running",
		}

		// Enrich from the process table where a main PID exists. A unit with
		// no MainPID (oneshot, or a group whose leader has exited) still
		// counts as running — systemd is the authority here, not gopsutil.
		if pid > 0 {
			if p, err := gopsprocess.NewProcessWithContext(ctx, int32(pid)); err == nil {
				if cpu, err := p.CPUPercentWithContext(ctx); err == nil {
					proc.CPUPercent = cpu
				}
				if mem, err := p.MemoryInfoWithContext(ctx); err == nil && mem != nil {
					proc.MemoryMB = float64(mem.RSS) / 1024 / 1024
				}
				if created, err := p.CreateTimeWithContext(ctx); err == nil {
					proc.UptimeSeconds = int64(time.Since(time.Unix(created/1000, 0)).Seconds())
				}
			}
		}

		matches = append(matches, proc)
	}

	return matches, nil
}

// listUnits expands a pattern to concrete loaded unit names.
func listUnits(ctx context.Context, pattern string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, unitQueryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", "list-units", "--all", "--plain",
		"--no-legend", "--no-pager", "--type=service", pattern)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("systemctl list-units timed out after %s", unitQueryTimeout)
		}
		return nil, fmt.Errorf("systemctl list-units: %w", err)
	}

	var units []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		units = append(units, fields[0])
	}
	return units, nil
}

// showUnit reads the properties needed to decide whether a unit is running.
func showUnit(ctx context.Context, unit string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, unitQueryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", "show", unit,
		"--property=ActiveState", "--property=SubState", "--property=MainPID", "--no-pager")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("systemctl show %s timed out after %s", unit, unitQueryTimeout)
		}
		return nil, fmt.Errorf("systemctl show %s: %w", unit, err)
	}

	props := make(map[string]string, 3)
	for _, line := range strings.Split(string(out), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			props[key] = value
		}
	}
	return props, nil
}
