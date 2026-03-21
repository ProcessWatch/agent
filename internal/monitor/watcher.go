package monitor

import (
	"context"
	"time"

	"github.com/ethan-mdev/service-watch/internal/alerting"
	"github.com/ethan-mdev/service-watch/internal/config"
	"github.com/ethan-mdev/service-watch/internal/core"
	"github.com/ethan-mdev/service-watch/internal/logger"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	gopsprocess "github.com/shirou/gopsutil/v4/process"
)

// Start begins the monitor loop that periodically checks the status of watchlist entries
func Start(
	ctx context.Context,
	cfg *config.Config,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
	log *logger.Logger,
	statusCh chan<- []core.WatchStatus,
	notifier *alerting.DiscordNotifier,
) {
	log.Info("watcher_started", map[string]interface{}{
		"pollIntervalSecs": cfg.PollIntervalSecs,
	})

	ticker := time.NewTicker(time.Duration(cfg.PollIntervalSecs) * time.Second)
	defer ticker.Stop()

	// prevState tracks the last known running state per process name so we can
	// log transitions (running→stopped, stopped→running) at info level without
	// spamming every poll cycle.
	prevState := make(map[string]bool)

	// Run immediately on startup, then on each tick.
	poll(ctx, cfg, watchlistMgr, processMgr, log, statusCh, prevState, notifier)

	for {
		select {
		case <-ctx.Done():
			log.Info("watcher_stopped", nil)
			return
		case <-ticker.C:
			poll(ctx, cfg, watchlistMgr, processMgr, log, statusCh, prevState, notifier)
		}
	}
}

func poll(
	ctx context.Context,
	cfg *config.Config,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
	log *logger.Logger,
	statusCh chan<- []core.WatchStatus,
	prevState map[string]bool,
	notifier *alerting.DiscordNotifier,
) {
	entries, err := watchlistMgr.List(ctx)
	if err != nil {
		log.Error("watcher_list_failed", map[string]interface{}{"error": err.Error()})
		return
	}

	statuses := make([]core.WatchStatus, 0, len(entries))

	for _, entry := range entries {
		status := buildStatus(ctx, cfg, entry, watchlistMgr, processMgr, log, notifier)

		// Log state transitions at info level.
		wasRunning, seen := prevState[entry.Name]
		if !seen {
			// First poll — always report initial state at info.
			if status.Running {
				log.Info("process_up", map[string]interface{}{"name": entry.Name, "pid": status.Process.PID})
			} else {
				log.Info("process_down", map[string]interface{}{"name": entry.Name})
				notifyAlert(ctx, cfg, notifier, log, alerting.Event{
					Type:         alerting.EventProcessDown,
					ProcessName:  entry.Name,
					ProjectLabel: cfg.Alerts.ProjectLabel,
					Message:      "process is down",
				})
			}
		} else if status.Running && !wasRunning {
			log.Info("process_up", map[string]interface{}{"name": entry.Name, "pid": status.Process.PID})
		} else if !status.Running && wasRunning {
			log.Info("process_down", map[string]interface{}{"name": entry.Name})
			notifyAlert(ctx, cfg, notifier, log, alerting.Event{
				Type:         alerting.EventProcessDown,
				ProcessName:  entry.Name,
				ProjectLabel: cfg.Alerts.ProjectLabel,
				Message:      "process transitioned to down",
			})
		}
		prevState[entry.Name] = status.Running

		statuses = append(statuses, status)
	}

	hostCPU, hostMemPct := sampleHostResources()
	log.Debug("host_resources", map[string]interface{}{
		"cpuPercent":        hostCPU,
		"memoryUsedPercent": hostMemPct,
	})

	// Non-blocking send, if nobody is consuming (e.g. TUI not yet started) we skip.
	select {
	case statusCh <- statuses:
	default:
	}
}

func buildStatus(
	ctx context.Context,
	cfg *config.Config,
	entry core.WatchlistItem,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
	log *logger.Logger,
	notifier *alerting.DiscordNotifier,
) core.WatchStatus {
	status := core.WatchStatus{Entry: entry}

	// Liveness check with PID pinning
	running, liveProc := checkLiveness(ctx, entry, watchlistMgr, processMgr)
	status.Running = running
	status.Process = liveProc

	if running {
		log.Debug("process_status", map[string]interface{}{
			"name":       entry.Name,
			"pid":        liveProc.PID,
			"cpuPercent": liveProc.CPUPercent,
			"memoryMB":   liveProc.MemoryMB,
		})
		return status
	}

	if !entry.AutoRestart {
		return status
	}

	// Auto-restart disabled after exceeding maxRetries.
	if entry.FailCount >= entry.MaxRetries && entry.MaxRetries > 0 {
		log.Error("process_max_retries_exceeded", map[string]interface{}{
			"name":       entry.Name,
			"failCount":  entry.FailCount,
			"maxRetries": entry.MaxRetries,
		})
		notifyAlert(ctx, cfg, notifier, log, alerting.Event{
			Type:         alerting.EventProcessMaxRetriesExceeded,
			ProcessName:  entry.Name,
			ProjectLabel: cfg.Alerts.ProjectLabel,
			Message:      "process exceeded max restart retries",
		})
		// Disable auto-restart to stop repeated alerting.
		watchlistMgr.Update(ctx, entry.Name, false)
		return status
	}

	// Cooldown check
	if entry.LastRestart != "" && entry.CooldownSecs > 0 {
		if lastRestart, err := time.Parse(time.RFC3339, entry.LastRestart); err == nil {
			elapsed := time.Since(lastRestart)
			cooldown := time.Duration(entry.CooldownSecs) * time.Second
			if elapsed < cooldown {
				remaining := int(cooldown.Seconds() - elapsed.Seconds())
				status.InCooldown = true
				status.CooldownRemaining = remaining
				log.Debug("process_in_cooldown", map[string]interface{}{
					"name":              entry.Name,
					"cooldownRemaining": remaining,
				})
				return status
			}
		}
	}

	// Attempt restart
	log.Info("restart_attempt", map[string]interface{}{
		"name":       entry.Name,
		"restartCmd": entry.RestartCmd,
	})

	if err := processMgr.Restart(ctx, entry.RestartCmd); err != nil {
		log.Error("restart_failed", map[string]interface{}{
			"name":  entry.Name,
			"error": err.Error(),
		})
		notifyAlert(ctx, cfg, notifier, log, alerting.Event{
			Type:         alerting.EventRestartFailed,
			ProcessName:  entry.Name,
			ProjectLabel: cfg.Alerts.ProjectLabel,
			Message:      "restart command failed",
			Error:        err.Error(),
		})
		watchlistMgr.IncrementFailCount(ctx, entry.Name)
		return status
	}

	// Post-restart health verification
	if cfg.RestartVerifyDelaySecs > 0 {
		time.Sleep(time.Duration(cfg.RestartVerifyDelaySecs) * time.Second)
	}

	stillRunning, verifiedProc := checkLiveness(ctx, entry, watchlistMgr, processMgr)
	if !stillRunning {
		log.Error("restart_verify_failed", map[string]interface{}{
			"name": entry.Name,
		})
		watchlistMgr.IncrementFailCount(ctx, entry.Name)
		return status
	}

	// Restart verified, update counts and tracked PID.
	watchlistMgr.IncrementRestartCount(ctx, entry.Name)
	watchlistMgr.ResetFailCount(ctx, entry.Name)
	if verifiedProc != nil {
		watchlistMgr.SetTrackedPID(ctx, entry.Name, verifiedProc.PID)
	}

	log.Info("restart_success", map[string]interface{}{
		"name": entry.Name,
		"pid": func() int32 {
			if verifiedProc != nil {
				return verifiedProc.PID
			}
			return 0
		}(),
	})
	notifyAlert(ctx, cfg, notifier, log, alerting.Event{
		Type:         alerting.EventRestartSuccess,
		ProcessName:  entry.Name,
		ProjectLabel: cfg.Alerts.ProjectLabel,
		Message:      "restart verified successfully",
	})

	status.Running = true
	status.Process = verifiedProc
	return status
}

func notifyAlert(
	ctx context.Context,
	cfg *config.Config,
	notifier *alerting.DiscordNotifier,
	log *logger.Logger,
	event alerting.Event,
) {
	if cfg == nil || !cfg.Alerts.Enabled || notifier == nil {
		return
	}
	if event.ProjectLabel == "" {
		event.ProjectLabel = cfg.Alerts.ProjectLabel
	}
	if err := notifier.Notify(ctx, event); err != nil {
		log.Error("alert_notify_failed", map[string]interface{}{
			"event":   string(event.Type),
			"process": event.ProcessName,
			"error":   err.Error(),
		})
	}
}

// checkLiveness checks if the process is running using PID pinning with a name-based fallback. Updates the tracked PID if the pinned PID is stale.
func checkLiveness(
	ctx context.Context,
	entry core.WatchlistItem,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
) (bool, *core.Process) {
	// Try pinned PID first.
	if pid, err := watchlistMgr.GetTrackedPID(ctx, entry.Name); err == nil && pid > 0 {
		if p, err := gopsprocess.NewProcessWithContext(ctx, pid); err == nil {
			if alive, err := p.IsRunningWithContext(ctx); err == nil && alive {
				proc := pidToProcess(ctx, p)
				return true, proc
			}
		}
		// Pinned PID is stale — fall through to name-based lookup.
	}

	// Name-based fallback.
	matches, err := processMgr.Find(ctx, entry.Name)
	if err != nil || len(matches) == 0 {
		return false, nil
	}

	// Pin the first match going forward.
	watchlistMgr.SetTrackedPID(ctx, entry.Name, matches[0].PID)
	return true, &matches[0]
}

// pidToProcess builds a core.Process from a live gopsutil process handle.
func pidToProcess(ctx context.Context, p *gopsprocess.Process) *core.Process {
	name, _ := p.NameWithContext(ctx)
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

	return &core.Process{
		Name:          name,
		PID:           p.Pid,
		State:         "running",
		CPUPercent:    cpuPct,
		MemoryMB:      memMB,
		UptimeSeconds: uptimeSecs,
	}
}

func sampleHostResources() (cpuPercent float64, memUsedPercent float64) {
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		cpuPercent = pcts[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		memUsedPercent = vm.UsedPercent
	}
	return
}
