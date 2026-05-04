package monitor

import (
	"context"
	"time"

	"github.com/ethan-mdev/process-watch/internal/config"
	"github.com/ethan-mdev/process-watch/internal/core"
	"github.com/ethan-mdev/process-watch/internal/logger"
	"github.com/ethan-mdev/process-watch/internal/reporting"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	gopsprocess "github.com/shirou/gopsutil/v4/process"
)

func Start(
	ctx context.Context,
	cfg *config.Config,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
	log *logger.Logger,
	statusCh chan<- []core.WatchStatus,
	reporter *reporting.Reporter,
) {
	log.Info("watcher_started", map[string]interface{}{
		"pollIntervalSecs": cfg.PollIntervalSecs,
	})

	ticker := time.NewTicker(time.Duration(cfg.PollIntervalSecs) * time.Second)
	defer ticker.Stop()

	prevState := make(map[string]bool)

	poll(ctx, cfg, watchlistMgr, processMgr, log, statusCh, prevState, reporter)

	for {
		select {
		case <-ctx.Done():
			log.Info("watcher_stopped", nil)
			return
		case <-ticker.C:
			poll(ctx, cfg, watchlistMgr, processMgr, log, statusCh, prevState, reporter)
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
	reporter *reporting.Reporter,
) {
	entries, err := watchlistMgr.List(ctx)
	if err != nil {
		log.Error("watcher_list_failed", map[string]interface{}{"error": err.Error()})
		return
	}

	statuses := make([]core.WatchStatus, 0, len(entries))
	var events []core.ReportEvent

	for _, entry := range entries {
		status, entryEvents := buildStatus(ctx, cfg, entry, watchlistMgr, processMgr, log)

		wasRunning, seen := prevState[entry.Name]
		if !seen {
			if status.Running {
				log.Info("process_up", map[string]interface{}{"name": entry.Name, "pid": status.Process.PID})
			} else {
				log.Info("process_down", map[string]interface{}{"name": entry.Name})
			}
		} else if status.Running && !wasRunning {
			log.Info("process_up", map[string]interface{}{"name": entry.Name, "pid": status.Process.PID})
		} else if !status.Running && wasRunning {
			log.Info("process_down", map[string]interface{}{"name": entry.Name})
		}
		prevState[entry.Name] = status.Running

		events = append(events, entryEvents...)
		statuses = append(statuses, status)
	}

	hostCPU, hostMemPct := sampleHostResources()
	log.Debug("host_resources", map[string]interface{}{
		"cpuPercent":        hostCPU,
		"memoryUsedPercent": hostMemPct,
	})

	select {
	case statusCh <- statuses:
	default:
	}

	if reporter != nil {
		if err := reporter.Send(ctx, statuses, events, hostCPU, hostMemPct, cfg.PollIntervalSecs); err != nil {
			log.Error("reporter_send_failed", map[string]interface{}{"error": err.Error()})
		}
	}
}

func buildStatus(
	ctx context.Context,
	cfg *config.Config,
	entry core.WatchlistItem,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
	log *logger.Logger,
) (core.WatchStatus, []core.ReportEvent) {
	status := core.WatchStatus{Entry: entry}
	var events []core.ReportEvent

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
		return status, events
	}

	if !entry.AutoRestart {
		events = append(events, core.ReportEvent{
			Time:    time.Now(),
			Type:    core.EventProcessDown,
			Process: entry.Name,
		})
		return status, events
	}

	// Process is down and we're about to act on it — emit process_down
	events = append(events, core.ReportEvent{
		Time:    time.Now(),
		Type:    core.EventProcessDown,
		Process: entry.Name,
	})

	if entry.FailCount >= entry.MaxRetries && entry.MaxRetries > 0 {
		log.Error("process_max_retries_exceeded", map[string]interface{}{
			"name":       entry.Name,
			"failCount":  entry.FailCount,
			"maxRetries": entry.MaxRetries,
		})
		events = append(events, core.ReportEvent{
			Time:    time.Now(),
			Type:    core.EventMaxRetriesExceeded,
			Process: entry.Name,
		})
		watchlistMgr.Update(ctx, entry.Name, false)
		return status, events
	}

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
				return status, events
			}
		}
	}

	log.Info("restart_attempt", map[string]interface{}{
		"name":       entry.Name,
		"restartCmd": entry.RestartCmd,
	})
	events = append(events, core.ReportEvent{
		Time:    time.Now(),
		Type:    core.EventRestartAttempt,
		Process: entry.Name,
	})

	if err := processMgr.Restart(ctx, entry.RestartCmd); err != nil {
		log.Error("restart_failed", map[string]interface{}{
			"name":  entry.Name,
			"error": err.Error(),
		})
		events = append(events, core.ReportEvent{
			Time:    time.Now(),
			Type:    core.EventRestartFailed,
			Process: entry.Name,
		})
		watchlistMgr.IncrementFailCount(ctx, entry.Name)
		return status, events
	}

	if cfg.RestartVerifyDelaySecs > 0 {
		time.Sleep(time.Duration(cfg.RestartVerifyDelaySecs) * time.Second)
	}

	stillRunning, verifiedProc := checkLiveness(ctx, entry, watchlistMgr, processMgr)
	if !stillRunning {
		log.Error("restart_verify_failed", map[string]interface{}{"name": entry.Name})
		events = append(events, core.ReportEvent{
			Time:    time.Now(),
			Type:    core.EventRestartVerifyFailed,
			Process: entry.Name,
		})
		watchlistMgr.IncrementFailCount(ctx, entry.Name)
		return status, events
	}

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
	events = append(events, core.ReportEvent{
		Time:    time.Now(),
		Type:    core.EventRestartSuccess,
		Process: entry.Name,
	})

	status.Running = true
	status.Process = verifiedProc
	return status, events
}

func checkLiveness(
	ctx context.Context,
	entry core.WatchlistItem,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
) (bool, *core.Process) {
	if pid, err := watchlistMgr.GetTrackedPID(ctx, entry.Name); err == nil && pid > 0 {
		if p, err := gopsprocess.NewProcessWithContext(ctx, pid); err == nil {
			if alive, err := p.IsRunningWithContext(ctx); err == nil && alive {
				proc := pidToProcess(ctx, p)
				return true, proc
			}
		}
	}

	matches, err := processMgr.Find(ctx, entry.Name)
	if err != nil || len(matches) == 0 {
		return false, nil
	}

	watchlistMgr.SetTrackedPID(ctx, entry.Name, matches[0].PID)
	return true, &matches[0]
}

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
