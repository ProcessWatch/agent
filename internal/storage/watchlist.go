package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ethan-mdev/service-watch/internal/core"
)

type jsonWatchlist struct {
	mutex       sync.RWMutex
	filepath    string
	items       map[string]*core.WatchlistItem
	trackedPIDs map[string]int32 // ephemeral, not persisted
}

func NewJSONWatchlist(filepath string) core.WatchlistManager {
	wl := &jsonWatchlist{
		filepath:    filepath,
		items:       make(map[string]*core.WatchlistItem),
		trackedPIDs: make(map[string]int32),
	}
	wl.load()
	return wl
}

// List implements core.WatchlistManager.
func (j *jsonWatchlist) List(ctx context.Context) ([]core.WatchlistItem, error) {
	j.mutex.RLock()
	defer j.mutex.RUnlock()

	items := make([]core.WatchlistItem, 0, len(j.items))
	for _, item := range j.items {
		items = append(items, *item)
	}
	return items, nil
}

// Get implements core.WatchlistManager.
func (j *jsonWatchlist) Get(ctx context.Context, name string) (core.WatchlistItem, error) {
	j.mutex.RLock()
	defer j.mutex.RUnlock()

	item, exists := j.items[name]
	if !exists {
		return core.WatchlistItem{}, fmt.Errorf("process not in watchlist: %s", name)
	}
	return *item, nil
}

// Add implements core.WatchlistManager.
func (j *jsonWatchlist) Add(ctx context.Context, entry core.WatchlistItem) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	if _, exists := j.items[entry.Name]; exists {
		return fmt.Errorf("process already in watchlist: %s", entry.Name)
	}

	j.items[entry.Name] = &entry
	return j.save()
}

// Remove implements core.WatchlistManager.
func (j *jsonWatchlist) Remove(ctx context.Context, name string) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	if _, exists := j.items[name]; !exists {
		return fmt.Errorf("process not in watchlist: %s", name)
	}

	delete(j.items, name)
	delete(j.trackedPIDs, name)
	return j.save()
}

// Update implements core.WatchlistManager.
func (j *jsonWatchlist) Update(ctx context.Context, name string, autoRestart bool) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	item, exists := j.items[name]
	if !exists {
		return fmt.Errorf("process not in watchlist: %s", name)
	}
	item.AutoRestart = autoRestart
	return j.save()
}

// IncrementRestartCount implements core.WatchlistManager.
func (j *jsonWatchlist) IncrementRestartCount(ctx context.Context, name string) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	item, exists := j.items[name]
	if !exists {
		return fmt.Errorf("process not in watchlist: %s", name)
	}
	item.RestartCount++
	item.LastRestart = time.Now().Format(time.RFC3339)
	return j.save()
}

// IncrementFailCount implements core.WatchlistManager.
func (j *jsonWatchlist) IncrementFailCount(ctx context.Context, name string) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	item, exists := j.items[name]
	if !exists {
		return fmt.Errorf("process not in watchlist: %s", name)
	}
	item.FailCount++
	return j.save()
}

// ResetFailCount implements core.WatchlistManager.
func (j *jsonWatchlist) ResetFailCount(ctx context.Context, name string) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	item, exists := j.items[name]
	if !exists {
		return fmt.Errorf("process not in watchlist: %s", name)
	}
	item.FailCount = 0
	return j.save()
}

// SetTrackedPID implements core.WatchlistManager.
func (j *jsonWatchlist) SetTrackedPID(ctx context.Context, name string, pid int32) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	if _, exists := j.items[name]; !exists {
		return fmt.Errorf("process not in watchlist: %s", name)
	}
	j.trackedPIDs[name] = pid
	return nil
}

// GetTrackedPID implements core.WatchlistManager.
func (j *jsonWatchlist) GetTrackedPID(ctx context.Context, name string) (int32, error) {
	j.mutex.RLock()
	defer j.mutex.RUnlock()

	if _, exists := j.items[name]; !exists {
		return 0, fmt.Errorf("process not in watchlist: %s", name)
	}
	return j.trackedPIDs[name], nil
}

func (j *jsonWatchlist) load() {
	data, err := os.ReadFile(j.filepath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		return
	}

	var items []core.WatchlistItem
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}

	for i := range items {
		j.items[items[i].Name] = &items[i]
	}
}

// save writes the watchlist to disk atomically using a temp file + rename.
// Caller must hold j.mutex (write lock).
func (j *jsonWatchlist) save() error {
	items := make([]core.WatchlistItem, 0, len(j.items))
	for _, item := range j.items {
		items = append(items, *item)
	}

	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling watchlist: %w", err)
	}

	tmp := j.filepath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("writing watchlist temp file: %w", err)
	}
	if err := os.Rename(tmp, j.filepath); err != nil {
		return fmt.Errorf("renaming watchlist temp file: %w", err)
	}
	return nil
}
