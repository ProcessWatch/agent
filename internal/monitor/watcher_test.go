package monitor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ethan-mdev/process-watch/internal/config"
	"github.com/ethan-mdev/process-watch/internal/core"
	"github.com/ethan-mdev/process-watch/internal/logger"
)

func TestCheckLivenessPinsRepresentativePID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc", true)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{testProcess(entry.Name, 42)}},
		},
	}

	running, proc, found := checkLiveness(ctx, entry, watchlist, processMgr)
	if !running {
		t.Fatalf("running = false, want true")
	}
	if proc == nil || proc.PID != 42 {
		t.Fatalf("proc = %+v, want PID 42", proc)
	}
	if found != 1 {
		t.Fatalf("found = %d, want 1", found)
	}

	pinned, err := watchlist.GetTrackedPID(ctx, entry.Name)
	if err != nil {
		t.Fatalf("GetTrackedPID() returned error: %v", err)
	}
	if pinned != 42 {
		t.Fatalf("tracked pid = %d, want 42", pinned)
	}
}

// A watchlist entry written before match modes existed must keep behaving
// exactly as it did: substring matching on the entry name.
func TestCheckLivenessDefaultsToSubstringOnLegacyEntry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := core.WatchlistItem{Name: "legacy-svc"} // no MatchMode, no Selector
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{testProcess(entry.Name, 7)}},
		},
	}

	running, _, _ := checkLiveness(ctx, entry, watchlist, processMgr)
	if !running {
		t.Fatalf("running = false, want true")
	}
	if got := processMgr.lastSelector; got.Mode != core.MatchSubstring || got.Value != entry.Name {
		t.Fatalf("selector = %+v, want {substring %s}", got, entry.Name)
	}
}

// Counting the whole match set is the point: three of four workers alive must
// read as running-but-degraded, not simply healthy.
func TestCheckLivenessCountsEveryMatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("gt-web", false)
	entry.ExpectedCount = 4
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{
				testProcess(entry.Name, 11),
				testProcess(entry.Name, 12),
				testProcess(entry.Name, 13),
			}},
		},
	}

	running, _, found := checkLiveness(ctx, entry, watchlist, processMgr)
	if !running {
		t.Fatalf("running = false, want true")
	}
	if found != 3 {
		t.Fatalf("found = %d, want 3", found)
	}

	status := core.WatchStatus{Running: running, Found: found, Expected: entry.ExpectedCount}
	if !status.Degraded() {
		t.Fatalf("Degraded() = false with %d of %d running, want true", found, entry.ExpectedCount)
	}
}

// An entry that never declared an expected count must not have one invented
// for it. A legacy substring entry matching several processes would otherwise
// report e.g. 3-of-1 and raise an incident about nothing.
func TestUndeclaredExpectedCountIsNotDegraded(t *testing.T) {
	t.Parallel()

	entry := core.WatchlistItem{Name: "legacy-node"} // no ExpectedCount
	status := core.WatchStatus{
		Running:  true,
		Found:    3, // substring matched three unrelated processes
		Expected: entry.ExpectedCount,
	}

	if status.Degraded() {
		t.Fatalf("Degraded() = true for an entry with no declared expectation, want false")
	}
	if status.Expected != 0 {
		t.Fatalf("Expected = %d, want 0 so the dashboard skips the count check", status.Expected)
	}
}

func TestBuildStatusRunningProcessReturnsRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-running", true)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{testProcess(entry.Name, 100)}},
		},
	}

	status, events := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t))
	if !status.Running {
		t.Fatalf("status.Running = false, want true")
	}
	if status.Process == nil || status.Process.PID != 100 {
		t.Fatalf("status.Process = %+v, want PID 100", status.Process)
	}
	if processMgr.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", processMgr.restartCalls)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
}

func TestBuildStatusAutoRestartDisabled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-disabled", false)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}},
		},
	}

	status, events := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t))
	if status.Running {
		t.Fatalf("status.Running = true, want false")
	}
	if processMgr.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", processMgr.restartCalls)
	}
	if len(events) != 1 || events[0].Type != core.EventProcessDown {
		t.Fatalf("events = %+v, want [process_down]", events)
	}
}

func TestBuildStatusMaxRetriesExceededDisablesAutoRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-max-retries", true)
	entry.FailCount = 3
	entry.MaxRetries = 3
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}},
		},
	}

	status, events := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t))
	if status.Running {
		t.Fatalf("status.Running = true, want false")
	}
	if watchlist.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", watchlist.updateCalls)
	}
	if watchlist.items[entry.Name].AutoRestart {
		t.Fatalf("AutoRestart = true, want false")
	}
	if processMgr.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", processMgr.restartCalls)
	}
	if len(events) != 2 || events[0].Type != core.EventProcessDown || events[1].Type != core.EventMaxRetriesExceeded {
		t.Fatalf("events = %+v, want [process_down, max_retries_exceeded]", events)
	}
}

func TestBuildStatusCooldownSkipsRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-cooldown", true)
	entry.LastRestart = time.Now().Format(time.RFC3339)
	entry.CooldownSecs = 60
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}},
		},
	}

	status, events := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t))
	if !status.InCooldown {
		t.Fatalf("status.InCooldown = false, want true")
	}
	if status.CooldownRemaining <= 0 {
		t.Fatalf("status.CooldownRemaining = %d, want > 0", status.CooldownRemaining)
	}
	if processMgr.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", processMgr.restartCalls)
	}
	if len(events) != 1 || events[0].Type != core.EventProcessDown {
		t.Fatalf("events = %+v, want [process_down]", events)
	}
}

func TestBuildStatusRestartFailureIncrementsFailCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-restart-fail", true)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}},
		},
		restartErr: errors.New("boom"),
	}

	status, events := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t))
	if status.Running {
		t.Fatalf("status.Running = true, want false")
	}
	if processMgr.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", processMgr.restartCalls)
	}
	if watchlist.incrementFailCalls != 1 {
		t.Fatalf("increment fail calls = %d, want 1", watchlist.incrementFailCalls)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3 (process_down + restart_attempt + restart_failed)", len(events))
	}
	if events[0].Type != core.EventProcessDown {
		t.Fatalf("events[0].Type = %q, want process_down", events[0].Type)
	}
	if events[1].Type != core.EventRestartAttempt {
		t.Fatalf("events[1].Type = %q, want restart_attempt", events[1].Type)
	}
	if events[2].Type != core.EventRestartFailed {
		t.Fatalf("events[2].Type = %q, want restart_failed", events[2].Type)
	}
}

func TestBuildStatusRestartVerifyFailureIncrementsFailCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-verify-fail", true)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}, {}},
		},
	}

	status, events := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t))
	if status.Running {
		t.Fatalf("status.Running = true, want false")
	}
	if processMgr.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", processMgr.restartCalls)
	}
	if watchlist.incrementFailCalls != 1 {
		t.Fatalf("increment fail calls = %d, want 1", watchlist.incrementFailCalls)
	}
	if watchlist.incrementRestartCalls != 0 {
		t.Fatalf("increment restart calls = %d, want 0", watchlist.incrementRestartCalls)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3 (process_down + restart_attempt + restart_verify_failed)", len(events))
	}
	if events[0].Type != core.EventProcessDown {
		t.Fatalf("events[0].Type = %q, want process_down", events[0].Type)
	}
	if events[1].Type != core.EventRestartAttempt {
		t.Fatalf("events[1].Type = %q, want restart_attempt", events[1].Type)
	}
	if events[2].Type != core.EventRestartVerifyFailed {
		t.Fatalf("events[2].Type = %q, want restart_verify_failed", events[2].Type)
	}
}

func TestBuildStatusRestartSuccessUpdatesState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-restart-success", true)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}, {testProcess(entry.Name, 99)}},
		},
	}

	status, events := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t))
	if !status.Running {
		t.Fatalf("status.Running = false, want true")
	}
	if status.Process == nil || status.Process.PID != 99 {
		t.Fatalf("status.Process = %+v, want PID 99", status.Process)
	}
	if processMgr.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", processMgr.restartCalls)
	}
	if watchlist.incrementRestartCalls != 1 {
		t.Fatalf("increment restart calls = %d, want 1", watchlist.incrementRestartCalls)
	}
	if watchlist.resetFailCalls != 1 {
		t.Fatalf("reset fail calls = %d, want 1", watchlist.resetFailCalls)
	}
	pinned, err := watchlist.GetTrackedPID(ctx, entry.Name)
	if err != nil {
		t.Fatalf("GetTrackedPID() returned error: %v", err)
	}
	if pinned != 99 {
		t.Fatalf("tracked pid = %d, want 99", pinned)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3 (process_down + restart_attempt + restart_success)", len(events))
	}
	if events[0].Type != core.EventProcessDown {
		t.Fatalf("events[0].Type = %q, want process_down", events[0].Type)
	}
	if events[1].Type != core.EventRestartAttempt {
		t.Fatalf("events[1].Type = %q, want restart_attempt", events[1].Type)
	}
	if events[2].Type != core.EventRestartSuccess {
		t.Fatalf("events[2].Type = %q, want restart_success", events[2].Type)
	}
}

func TestHasRecovered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		running    bool
		wasRunning bool
		seen       bool
		want       bool
	}{
		// The transition that matters: a process the agent watched go down is
		// running again, whether the agent, systemd, or a human restarted it.
		{"down then up", true, false, true, true},

		// First sighting after the agent starts. Reported so an incident
		// opened before an agent restart still gets closed.
		{"first sighting running", true, false, false, true},
		{"first sighting down", false, false, false, false},

		// Steady states emit nothing, so a long-running process does not
		// re-report recovery on every poll.
		{"still running", true, true, true, false},
		{"still down", false, false, true, false},

		// Going down is process_down's job, not this one's.
		{"up then down", false, true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hasRecovered(tt.running, tt.wasRunning, tt.seen); got != tt.want {
				t.Fatalf("hasRecovered(running=%v, wasRunning=%v, seen=%v) = %v, want %v",
					tt.running, tt.wasRunning, tt.seen, got, tt.want)
			}
		})
	}
}

func TestReportBufferAccumulatesEventsBetweenSends(t *testing.T) {
	t.Parallel()

	buf := newReportBuffer()
	statusA := []core.WatchStatus{{Running: false}}
	statusB := []core.WatchStatus{{Running: true}}

	// Several polls happen between two reports.
	buf.update(statusA, []core.ReportEvent{{Type: core.EventProcessDown, Process: "a"}}, 10, 20)
	buf.update(statusB, []core.ReportEvent{{Type: core.EventProcessRecovered, Process: "a"}}, 30, 40)

	statuses, events, cpu, mem, ok := buf.take()
	if !ok {
		t.Fatal("take() ok = false, want true")
	}
	// Statuses are current-state: the latest wins.
	if len(statuses) != 1 || !statuses[0].Running {
		t.Fatalf("statuses = %+v, want the newest snapshot", statuses)
	}
	if cpu != 30 || mem != 40 {
		t.Fatalf("cpu/mem = %v/%v, want 30/40", cpu, mem)
	}
	// Events are history: none may be dropped.
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (down then recovered)", len(events))
	}
	if events[0].Type != core.EventProcessDown || events[1].Type != core.EventProcessRecovered {
		t.Fatalf("event order = %q,%q, want process_down,process_recovered", events[0].Type, events[1].Type)
	}

	// A drained buffer still reports the last snapshot, with no repeat events.
	_, events, _, _, ok = buf.take()
	if !ok || len(events) != 0 {
		t.Fatalf("second take: ok=%v events=%d, want ok=true events=0", ok, len(events))
	}
}

func TestReportBufferRequeuePreservesOrder(t *testing.T) {
	t.Parallel()

	buf := newReportBuffer()
	buf.update(nil, []core.ReportEvent{{Process: "first"}}, 0, 0)

	_, events, _, _, _ := buf.take()
	buf.update(nil, []core.ReportEvent{{Process: "second"}}, 0, 0)
	buf.requeue(events) // the send failed

	_, events, _, _, _ = buf.take()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Process != "first" || events[1].Process != "second" {
		t.Fatalf("order = %q,%q, want first,second", events[0].Process, events[1].Process)
	}
}

func TestReportBufferBoundsGrowth(t *testing.T) {
	t.Parallel()

	buf := newReportBuffer()
	for i := 0; i < maxBufferedEvents+50; i++ {
		buf.update(nil, []core.ReportEvent{{Process: fmt.Sprintf("evt-%d", i)}}, 0, 0)
	}

	_, events, _, _, _ := buf.take()
	if len(events) != maxBufferedEvents {
		t.Fatalf("events = %d, want capped at %d", len(events), maxBufferedEvents)
	}
	// Oldest are dropped, so the most recent transition always survives.
	last := events[len(events)-1].Process
	if want := fmt.Sprintf("evt-%d", maxBufferedEvents+49); last != want {
		t.Fatalf("newest retained = %q, want %q", last, want)
	}
}

func testConfig() *config.Config {
	return &config.Config{
		PollIntervalSecs:       1,
		RestartVerifyDelaySecs: 0,
		LogLevel:               "debug",
	}
}

func testLogger(t *testing.T) *logger.Logger {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, err := logger.Start(path, "debug")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	l.SetQuiet(true)
	t.Cleanup(func() {
		_ = l.Close()
	})
	return l
}

func testWatchEntry(name string, autoRestart bool) core.WatchlistItem {
	return core.WatchlistItem{
		Name:         name,
		RestartCmd:   "echo restart",
		AutoRestart:  autoRestart,
		MaxRetries:   3,
		CooldownSecs: 0,
	}
}

func testProcess(name string, pid int32) core.Process {
	return core.Process{
		Name:       name,
		PID:        pid,
		State:      "running",
		CPUPercent: 1.0,
		MemoryMB:   10.0,
	}
}

type fakeProcessManager struct {
	mu           sync.Mutex
	findQueues   map[string][][]core.Process
	lastSelector core.Selector
	restartErr   error
	restartCalls int
}

func (f *fakeProcessManager) ListAll(ctx context.Context) ([]core.Process, error) {
	return nil, nil
}

// Match is keyed on the selector's resolved value, which for entries built by
// testWatchEntry is just the entry name.
func (f *fakeProcessManager) Match(ctx context.Context, sel core.Selector) ([]core.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastSelector = sel

	queues, ok := f.findQueues[sel.Value]
	if !ok || len(queues) == 0 {
		return nil, nil
	}
	next := queues[0]
	f.findQueues[sel.Value] = queues[1:]
	return next, nil
}

func (f *fakeProcessManager) Restart(ctx context.Context, restartCmd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartCalls++
	return f.restartErr
}

type fakeWatchlist struct {
	mu                    sync.RWMutex
	items                 map[string]core.WatchlistItem
	trackedPIDs           map[string]int32
	updateCalls           int
	incrementRestartCalls int
	incrementFailCalls    int
	resetFailCalls        int
}

func newFakeWatchlist() *fakeWatchlist {
	return &fakeWatchlist{
		items:       map[string]core.WatchlistItem{},
		trackedPIDs: map[string]int32{},
	}
}

func (f *fakeWatchlist) List(ctx context.Context) ([]core.WatchlistItem, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]core.WatchlistItem, 0, len(f.items))
	for _, item := range f.items {
		out = append(out, item)
	}
	return out, nil
}

func (f *fakeWatchlist) Get(ctx context.Context, name string) (core.WatchlistItem, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	item, ok := f.items[name]
	if !ok {
		return core.WatchlistItem{}, fmt.Errorf("not found: %s", name)
	}
	return item, nil
}

func (f *fakeWatchlist) Add(ctx context.Context, entry core.WatchlistItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[entry.Name] = entry
	return nil
}

func (f *fakeWatchlist) Remove(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, name)
	delete(f.trackedPIDs, name)
	return nil
}

func (f *fakeWatchlist) Update(ctx context.Context, name string, autoRestart bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[name]
	if !ok {
		return fmt.Errorf("not found: %s", name)
	}
	item.AutoRestart = autoRestart
	f.items[name] = item
	f.updateCalls++
	return nil
}

func (f *fakeWatchlist) IncrementRestartCount(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[name]
	if !ok {
		return fmt.Errorf("not found: %s", name)
	}
	item.RestartCount++
	item.LastRestart = time.Now().Format(time.RFC3339)
	f.items[name] = item
	f.incrementRestartCalls++
	return nil
}

func (f *fakeWatchlist) IncrementFailCount(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[name]
	if !ok {
		return fmt.Errorf("not found: %s", name)
	}
	item.FailCount++
	item.LastRestart = time.Now().Format(time.RFC3339)
	f.items[name] = item
	f.incrementFailCalls++
	return nil
}

func (f *fakeWatchlist) ResetFailCount(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[name]
	if !ok {
		return fmt.Errorf("not found: %s", name)
	}
	item.FailCount = 0
	f.items[name] = item
	f.resetFailCalls++
	return nil
}

func (f *fakeWatchlist) SetTrackedPID(ctx context.Context, name string, pid int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[name]; !ok {
		return fmt.Errorf("not found: %s", name)
	}
	f.trackedPIDs[name] = pid
	return nil
}

func (f *fakeWatchlist) GetTrackedPID(ctx context.Context, name string) (int32, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.items[name]; !ok {
		return 0, fmt.Errorf("not found: %s", name)
	}
	return f.trackedPIDs[name], nil
}
