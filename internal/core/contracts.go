package core

import "context"

// ProcessManager abstracts OS service control.
type ProcessManager interface {
	ListAll(ctx context.Context) ([]Process, error)           // all OS processes
	Find(ctx context.Context, name string) ([]Process, error) // match by name (may return multiple PIDs)
	IsRunning(ctx context.Context, name string) (bool, error)
	Restart(ctx context.Context, restartCmd string) error // exec.Command via shell
}

// WatchlistManager abstracts watchlist management.
type WatchlistManager interface {
	// Lists all watchlist items with current service details populated.
	List(ctx context.Context) ([]WatchlistItem, error)
	// Gets a specific watchlist item by service name with current service details.
	Get(ctx context.Context, name string) (WatchlistItem, error)
	// Adds a process to the watchlist.
	Add(ctx context.Context, entry WatchlistItem) error
	// Removes a service from the watchlist.
	Remove(ctx context.Context, name string) error
	// Updates the auto-restart setting for a watchlist item.
	Update(ctx context.Context, name string, autoRestart bool) error
	// Increments the restart count and last restart time for a watchlist item.
	IncrementRestartCount(ctx context.Context, name string) error
	// Increments the failure count for a watchlist item.
	IncrementFailCount(ctx context.Context, name string) error
	// Resets the failure count for a watchlist item (e.g., after a successful restart).
	ResetFailCount(ctx context.Context, name string) error
	// SetTrackedPID stores the PID for a watchlist entry in memory only (not persisted).
	SetTrackedPID(ctx context.Context, name string, pid int32) error
	// GetTrackedPID returns the last known PID for a watchlist entry, or 0 if unset.
	GetTrackedPID(ctx context.Context, name string) (int32, error)
}
