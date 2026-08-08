// Package monitor implements the agent's polling loop: each cycle it checks
// the liveness of every watchlist entry, runs recovery commands where needed,
// and publishes a status snapshot plus any lifecycle events that occurred.
package monitor

import (
	"context"
	"sync"
	"time"

	"github.com/ethan-mdev/process-watch/internal/config"
	"github.com/ethan-mdev/process-watch/internal/core"
	"github.com/ethan-mdev/process-watch/internal/logger"
	"github.com/ethan-mdev/process-watch/internal/reporting"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// Start runs the poll loop until ctx is cancelled. It is the only goroutine
// that runs recovery commands or mutates watchlist counters; the TUI reads
// the same watchlist but only ever adds/removes entries.
//
// Each cycle publishes a full status snapshot on statusCh with a non-blocking
// send: if the consumer is behind, the snapshot is dropped — the next cycle
// supersedes it anyway.
//
// Reporting runs on its own goroutine and its own, slower interval. The
// heartbeat POST used to run inline here with a 10s timeout, so an unreachable
// dashboard stalled local process checks for as long as the network took to
// give up. Local monitoring now continues at full speed regardless of what the
// network is doing.
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
		"pollIntervalSecs":   cfg.PollIntervalSecs,
		"reportIntervalSecs": cfg.ReportIntervalSecs,
	})

	ticker := time.NewTicker(time.Duration(cfg.PollIntervalSecs) * time.Second)
	defer ticker.Stop()

	prevState := make(map[string]bool)
	buf := newReportBuffer()

	if reporter != nil {
		go runReporter(ctx, cfg, log, reporter, buf)
	}

	poll(ctx, cfg, watchlistMgr, processMgr, log, statusCh, prevState, buf)

	for {
		select {
		case <-ctx.Done():
			log.Info("watcher_stopped", nil)
			return
		case <-ticker.C:
			poll(ctx, cfg, watchlistMgr, processMgr, log, statusCh, prevState, buf)
		}
	}
}

// maxBufferedEvents bounds the queue so a dashboard that is unreachable for
// hours cannot grow the agent's memory without limit. Oldest events are
// dropped first: a stale transition matters less than a recent one, and
// process_down re-emits every poll anyway.
const maxBufferedEvents = 500

// reportBuffer decouples the poll loop from the reporter. Poll writes the
// latest state and appends transitions; the reporter drains it on its own
// schedule.
type reportBuffer struct {
	mu       sync.Mutex
	statuses []core.WatchStatus
	events   []core.ReportEvent
	cpu      float64
	mem      float64
	haveData bool
}

func newReportBuffer() *reportBuffer {
	return &reportBuffer{}
}

// update stores the newest snapshot and queues any transitions. Statuses are
// current-state so the latest wins, but events are history and accumulate
// until they are successfully sent.
func (b *reportBuffer) update(statuses []core.WatchStatus, events []core.ReportEvent, cpu, mem float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.statuses = statuses
	b.cpu = cpu
	b.mem = mem
	b.haveData = true
	b.events = append(b.events, events...)
	if overflow := len(b.events) - maxBufferedEvents; overflow > 0 {
		b.events = b.events[overflow:]
	}
}

// take removes the current contents for a send attempt.
func (b *reportBuffer) take() (statuses []core.WatchStatus, events []core.ReportEvent, cpu, mem float64, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.haveData {
		return nil, nil, 0, 0, false
	}
	events = b.events
	b.events = nil
	return b.statuses, events, b.cpu, b.mem, true
}

// requeue puts events back after a failed send, ahead of anything queued in
// the meantime so ordering survives.
func (b *reportBuffer) requeue(events []core.ReportEvent) {
	if len(events) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.events = append(events, b.events...)
	if overflow := len(b.events) - maxBufferedEvents; overflow > 0 {
		b.events = b.events[overflow:]
	}
}

// runReporter sends heartbeats on its own interval, independent of polling.
func runReporter(
	ctx context.Context,
	cfg *config.Config,
	log *logger.Logger,
	reporter *reporting.Reporter,
	buf *reportBuffer,
) {
	ticker := time.NewTicker(time.Duration(cfg.ReportIntervalSecs) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			statuses, events, cpu, mem, ok := buf.take()
			if !ok {
				continue // no poll has completed yet
			}
			if err := reporter.Send(ctx, statuses, events, cpu, mem, cfg.ReportIntervalSecs); err != nil {
				// Transitions are one-shot, so losing them loses history —
				// unlike the snapshot, which the next cycle reproduces.
				buf.requeue(events)
				log.Error("reporter_send_failed", map[string]interface{}{
					"error":        err.Error(),
					"queuedEvents": len(events),
				})
			}
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
	buf *reportBuffer,
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

		recovered := hasRecovered(status.Running, wasRunning, seen)

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

		// Appended after the entry's own events so that when a recovery
		// command worked, restart_success still lands on the incident
		// timeline before process_recovered closes it.
		if recovered {
			events = append(events, core.ReportEvent{
				Time:    time.Now(),
				Type:    core.EventProcessRecovered,
				Process: entry.Name,
			})
		}

		statuses = append(statuses, status)
	}

	// Forget state for entries removed from the watchlist, so re-adding one
	// later logs a fresh up/down transition.
	current := make(map[string]bool, len(entries))
	for _, entry := range entries {
		current[entry.Name] = true
	}
	for name := range prevState {
		if !current[name] {
			delete(prevState, name)
		}
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

	// Hand off to the reporter goroutine. This never blocks on the network.
	buf.update(statuses, events, hostCPU, hostMemPct)
}

// hasRecovered reports whether this observation should emit a recovery event.
//
// A down→up transition is the obvious case. The first sighting of a running
// process also counts: the agent may have been restarted while an incident
// was open, and nothing else would ever close it. Reporting recovery for a
// process that was never down is harmless — ingest only acts on it when an
// open incident exists.
func hasRecovered(running, wasRunning, seen bool) bool {
	return running && (!seen || !wasRunning)
}

// buildStatus evaluates one entry: liveness first, then — when the process is
// down and auto-restart is enabled — the retry budget, the cooldown window,
// and finally the recovery command itself. It returns the entry's status and
// whatever lifecycle events the cycle produced, in the order they happened.
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

	running, liveProc, found := checkLiveness(ctx, entry, watchlistMgr, processMgr)
	status.Running = running
	status.Process = liveProc
	status.Found = found
	// The raw configured value, not Expected(). An entry that never declared
	// how many instances it wants must not have an expectation of 1 invented
	// for it — a legacy substring entry matching three processes would then
	// report 3-of-1 and raise an incident about nothing.
	status.Expected = entry.ExpectedCount

	if running {
		log.Debug("process_status", map[string]interface{}{
			"name":       entry.Name,
			"pid":        liveProc.PID,
			"cpuPercent": liveProc.CPUPercent,
			"memoryMB":   liveProc.MemoryMB,
			"found":      found,
			"expected":   status.Expected,
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

	stillRunning, verifiedProc, verifiedCount := checkLiveness(ctx, entry, watchlistMgr, processMgr)
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
	status.Found = verifiedCount
	return status, events
}

// checkLiveness resolves the entry's selector and reports how many instances
// are running, along with a representative process for display.
//
// The previous implementation pinned the PID of whichever process it happened
// to match first and re-pinned on every miss. Watching "node" therefore read
// as healthy for as long as *any* node process existed on the box, and a dead
// worker in a group of four was invisible. Counting the full match set is what
// makes an expected-instance check possible at all, so the pin is gone.
func checkLiveness(
	ctx context.Context,
	entry core.WatchlistItem,
	watchlistMgr core.WatchlistManager,
	processMgr core.ProcessManager,
) (bool, *core.Process, int) {
	matches, err := processMgr.Match(ctx, entry.ResolvedSelector())
	if err != nil || len(matches) == 0 {
		return false, nil, 0
	}

	// The representative is the longest-running match, so a flapping worker
	// joining the group does not make the entry's reported uptime jump around.
	rep := 0
	for i, m := range matches {
		if m.UptimeSeconds > matches[rep].UptimeSeconds {
			rep = i
		}
	}

	watchlistMgr.SetTrackedPID(ctx, entry.Name, matches[rep].PID)
	return true, &matches[rep], len(matches)
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
