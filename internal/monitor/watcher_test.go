package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethan-mdev/service-watch/internal/alerting"
	"github.com/ethan-mdev/service-watch/internal/config"
	"github.com/ethan-mdev/service-watch/internal/core"
	"github.com/ethan-mdev/service-watch/internal/logger"
)

func TestCheckLivenessNameFallbackPinsPID(t *testing.T) {
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

	running, proc := checkLiveness(ctx, entry, watchlist, processMgr)
	if !running {
		t.Fatalf("running = false, want true")
	}
	if proc == nil || proc.PID != 42 {
		t.Fatalf("proc = %+v, want PID 42", proc)
	}

	pinned, err := watchlist.GetTrackedPID(ctx, entry.Name)
	if err != nil {
		t.Fatalf("GetTrackedPID() returned error: %v", err)
	}
	if pinned != 42 {
		t.Fatalf("tracked pid = %d, want 42", pinned)
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

	status := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t), nil)
	if !status.Running {
		t.Fatalf("status.Running = false, want true")
	}
	if status.Process == nil || status.Process.PID != 100 {
		t.Fatalf("status.Process = %+v, want PID 100", status.Process)
	}
	if processMgr.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", processMgr.restartCalls)
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

	status := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t), nil)
	if status.Running {
		t.Fatalf("status.Running = true, want false")
	}
	if processMgr.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", processMgr.restartCalls)
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

	status := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t), nil)
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

	status := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t), nil)
	if !status.InCooldown {
		t.Fatalf("status.InCooldown = false, want true")
	}
	if status.CooldownRemaining <= 0 {
		t.Fatalf("status.CooldownRemaining = %d, want > 0", status.CooldownRemaining)
	}
	if processMgr.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0", processMgr.restartCalls)
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

	status := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t), nil)
	if status.Running {
		t.Fatalf("status.Running = true, want false")
	}
	if processMgr.restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", processMgr.restartCalls)
	}
	if watchlist.incrementFailCalls != 1 {
		t.Fatalf("increment fail calls = %d, want 1", watchlist.incrementFailCalls)
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

	status := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t), nil)
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

	status := buildStatus(ctx, testConfig(), entry, watchlist, processMgr, testLogger(t), nil)
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
}

func TestBuildStatusRestartFailedSendsAlertWhenEnabled(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_, _ = io.ReadAll(r.Body)
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	notifier, err := alerting.NewDiscordNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewDiscordNotifier() returned error: %v", err)
	}
	defer notifier.Close()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-restart-fail-alert", true)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}},
		},
		restartErr: errors.New("boom"),
	}

	cfg := testConfig()
	cfg.Alerts.Enabled = true
	cfg.Alerts.ProjectLabel = "test-project"

	_ = buildStatus(ctx, cfg, entry, watchlist, processMgr, testLogger(t), notifier)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := atomic.LoadInt32(&hits); got == 0 {
		t.Fatalf("expected at least 1 alert webhook hit, got %d", got)
	}
}

func TestBuildStatusRestartFailedDoesNotSendAlertWhenDisabled(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_, _ = io.ReadAll(r.Body)
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	notifier, err := alerting.NewDiscordNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewDiscordNotifier() returned error: %v", err)
	}
	defer notifier.Close()

	ctx := context.Background()
	watchlist := newFakeWatchlist()
	entry := testWatchEntry("svc-restart-fail-no-alert", true)
	watchlist.items[entry.Name] = entry

	processMgr := &fakeProcessManager{
		findQueues: map[string][][]core.Process{
			entry.Name: {{}},
		},
		restartErr: errors.New("boom"),
	}

	cfg := testConfig()
	cfg.Alerts.Enabled = false

	_ = buildStatus(ctx, cfg, entry, watchlist, processMgr, testLogger(t), notifier)
	time.Sleep(100 * time.Millisecond)

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("expected 0 alert webhook hits when disabled, got %d", got)
	}
}

func testConfig() *config.Config {
	return &config.Config{
		MetricsPort:            9090,
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
	restartErr   error
	restartCalls int
}

func (f *fakeProcessManager) ListAll(ctx context.Context) ([]core.Process, error) {
	return nil, nil
}

func (f *fakeProcessManager) Find(ctx context.Context, name string) ([]core.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	queues, ok := f.findQueues[name]
	if !ok || len(queues) == 0 {
		return nil, nil
	}
	next := queues[0]
	f.findQueues[name] = queues[1:]
	return next, nil
}

func (f *fakeProcessManager) IsRunning(ctx context.Context, name string) (bool, error) {
	return false, nil
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
