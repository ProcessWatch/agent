package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ethan-mdev/service-watch/internal/core"
	"github.com/ethan-mdev/service-watch/internal/tui/views"
)

type activeView int

const (
	showList activeView = iota
	showPicker
)

// StatusUpdateMsg carries the latest watcher snapshot.
type StatusUpdateMsg []core.WatchStatus

// RestartResultMsg is returned after a manual restart attempt from the TUI.
type RestartResultMsg struct {
	Name string
	Err  error
}

// waitForStatus blocks until the next status snapshot arrives on the channel.
func waitForStatus(ch <-chan []core.WatchStatus) tea.Cmd {
	return func() tea.Msg {
		statuses, ok := <-ch
		if !ok {
			return tea.Quit()
		}
		return StatusUpdateMsg(statuses)
	}
}

// Model is the top-level Bubble Tea model.
type Model struct {
	ctx        context.Context
	statusCh   <-chan []core.WatchStatus
	processMgr core.ProcessManager
	active     activeView
	list       views.ListModel
	picker     views.PickerModel
}

func New(
	ctx context.Context,
	statusCh <-chan []core.WatchStatus,
	watchlist core.WatchlistManager,
	processMgr core.ProcessManager,
) Model {
	return Model{
		ctx:        ctx,
		statusCh:   statusCh,
		processMgr: processMgr,
		active:     showList,
		list:       views.NewListModel(ctx, watchlist),
		picker:     views.NewPickerModel(ctx, processMgr, watchlist),
	}
}

func (m Model) Init() tea.Cmd {
	return waitForStatus(m.statusCh)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
		m.picker.SetSize(msg.Width, msg.Height)
		return m, nil

	case StatusUpdateMsg:
		m.list.SetStatuses([]core.WatchStatus(msg))
		return m, waitForStatus(m.statusCh)

	case views.SwitchToPickerMsg:
		m.active = showPicker
		return m, m.picker.Init()

	case views.SwitchToListMsg:
		m.active = showList
		return m, nil

	case views.RestartRequestMsg:
		entry := msg.Entry
		return m, func() tea.Msg {
			err := m.processMgr.Restart(m.ctx, entry.RestartCmd)
			return RestartResultMsg{Name: entry.Name, Err: err}
		}

	case RestartResultMsg:
		// Watcher will detect the state change on the next tick — no extra action needed.
		return m, nil

	case tea.KeyMsg:
		// q quits only from the list view; picker handles its own esc/q.
		if msg.String() == "q" && m.active == showList {
			return m, tea.Quit
		}
	}

	switch m.active {
	case showList:
		newList, cmd := m.list.Update(msg)
		m.list = newList
		return m, cmd
	case showPicker:
		newPicker, cmd := m.picker.Update(msg)
		m.picker = newPicker
		return m, cmd
	}

	return m, nil
}

func (m Model) View() string {
	if m.active == showPicker {
		return m.picker.View()
	}
	return m.list.View()
}
