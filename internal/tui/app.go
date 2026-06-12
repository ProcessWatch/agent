// Package tui hosts the Bubble Tea terminal UI. app.go bootstraps the
// program; model.go owns the top-level model that routes messages between
// the views and re-arms the listener for watcher status snapshots.
package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ethan-mdev/process-watch/internal/core"
)

// Run starts the Bubble Tea TUI and blocks until the user quits or ctx is cancelled.
func Run(
	ctx context.Context,
	statusCh <-chan []core.WatchStatus,
	watchlist core.WatchlistManager,
	processMgr core.ProcessManager,
) error {
	manager := New(ctx, statusCh, watchlist, processMgr)
	program := tea.NewProgram(manager, tea.WithAltScreen())

	// Graceful shutdown: when ctx is cancelled (SIGINT/SIGTERM), quit the TUI.
	go func() {
		<-ctx.Done()
		program.Quit()
	}()

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
