package views

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ethan-mdev/process-watch/internal/core"
)

type pendingAction int

const (
	actionNone pendingAction = iota
	actionRemove
	actionRestart
)

type confirmation struct {
	active  bool
	action  pendingAction
	name    string
	message string
}

func (m ListModel) Width() int  { return m.width }
func (m ListModel) Height() int { return m.height }

// Messages for cross-model communication

type SwitchToPickerMsg struct{}
type SwitchToListMsg struct{}
type SwitchToLogsMsg struct{}
type RestartRequestMsg struct{ Entry core.WatchlistItem }

// Styles

var (
	styleRunning  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleStopped  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleCooldown = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleBorder   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#a78bfa")).
			Padding(0, 1)
)

// List item

type statusItem struct{ status core.WatchStatus }

func (i statusItem) FilterValue() string { return i.status.Entry.Name }

// Custom delegate

type statusDelegate struct{}

func (d statusDelegate) Height() int                             { return 2 }
func (d statusDelegate) Spacing() int                            { return 1 }
func (d statusDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d statusDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	si, ok := item.(statusItem)
	if !ok {
		return
	}
	s := si.status

	var stateBadge string
	switch {
	case s.InCooldown:
		stateBadge = styleCooldown.Render(fmt.Sprintf("cooldown (%ds)", s.CooldownRemaining))
	case s.Running:
		stateBadge = styleRunning.Render("● running")
	default:
		stateBadge = styleStopped.Render("● stopped")
	}

	var details string
	if s.Process != nil {
		details = styleDim.Render(fmt.Sprintf(
			"PID %-6d  CPU %5.1f%%  Mem %6.1fMB",
			s.Process.PID, s.Process.CPUPercent, s.Process.MemoryMB,
		))
	} else {
		details = styleDim.Render("—")
	}

	name := s.Entry.Name
	if index == m.Index() {
		name = styleBold.Render("> " + name)
	} else {
		name = "  " + name
	}

	fmt.Fprintf(w, "%s  %s\n%s", name, stateBadge, "  "+details)
}

// Keybindings

type listKeyMap struct {
	Add     key.Binding
	Remove  key.Binding
	Restart key.Binding
	Debug   key.Binding
	Logs    key.Binding
}

var listKeys = listKeyMap{
	Add:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
	Remove:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove")),
	Restart: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
	Debug:   key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "debug info")),
	Logs:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "events")),
}

// ListModel

type ListModel struct {
	ctx       context.Context
	list      list.Model
	watchlist core.WatchlistManager
	statuses  []core.WatchStatus
	showDebug bool
	confirm   confirmation
	width     int
	height    int
}

func NewListModel(ctx context.Context, watchlist core.WatchlistManager) ListModel {
	l := list.New([]list.Item{}, statusDelegate{}, 0, 0)
	l.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a78bfa"))
	l.Title = "ProcessWatch"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ShowFullHelp = key.NewBinding()
	l.KeyMap.CloseFullHelp = key.NewBinding()
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{listKeys.Add, listKeys.Remove, listKeys.Restart, listKeys.Debug, listKeys.Logs}
	}
	return ListModel{
		ctx:       ctx,
		list:      l,
		watchlist: watchlist,
	}
}

func (m *ListModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h)
}

func (m *ListModel) SetStatuses(statuses []core.WatchStatus) {
	m.statuses = statuses
	items := make([]list.Item, len(statuses))
	for i, s := range statuses {
		items[i] = statusItem{status: s}
	}
	m.list.SetItems(items)
}

func (m ListModel) selectedStatus() (core.WatchStatus, bool) {
	item, ok := m.list.SelectedItem().(statusItem)
	if !ok {
		return core.WatchStatus{}, false
	}
	return item.status, true
}

func (m ListModel) Update(msg tea.Msg) (ListModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		if m.confirm.active {
			switch km.String() {
			case "y":
				switch m.confirm.action {
				case actionRemove:
					m.watchlist.Remove(m.ctx, m.confirm.name)
					m.confirm = confirmation{}
				case actionRestart:
					name := m.confirm.name
					m.confirm = confirmation{}
					if s, ok := m.selectedStatus(); ok && s.Entry.Name == name {
						e := s.Entry
						return m, func() tea.Msg { return RestartRequestMsg{Entry: e} }
					}
				}
				return m, nil
			case "n", "esc":
				m.confirm = confirmation{}
				return m, nil
			}
			return m, nil
		}

		switch {
		case key.Matches(km, listKeys.Add):
			return m, func() tea.Msg { return SwitchToPickerMsg{} }

		case key.Matches(km, listKeys.Remove):
			if s, ok := m.selectedStatus(); ok {
				m.showDebug = false
				m.confirm = confirmation{
					active:  true,
					action:  actionRemove,
					name:    s.Entry.Name,
					message: fmt.Sprintf("Remove \"%s\" from watchlist?", s.Entry.Name),
				}
			}

		case key.Matches(km, listKeys.Restart):
			if s, ok := m.selectedStatus(); ok {
				m.showDebug = false
				m.confirm = confirmation{
					active:  true,
					action:  actionRestart,
					name:    s.Entry.Name,
					message: fmt.Sprintf("Restart \"%s\"?", s.Entry.Name),
				}
			}

		case key.Matches(km, listKeys.Debug):
			m.confirm = confirmation{}
			m.showDebug = !m.showDebug

		case key.Matches(km, listKeys.Logs):
			return m, func() tea.Msg { return SwitchToLogsMsg{} }
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m ListModel) View() string {
	if m.width == 0 {
		return "loading..."
	}

	content := m.list.View()

	if m.confirm.active {
		panel := styleBorder.
			Width(m.width/3 - 2).
			Render(m.confirm.message + "\n\n" + styleDim.Render("y to confirm · n/esc to cancel"))
		content = lipgloss.JoinHorizontal(lipgloss.Top, content, panel)
	} else if m.showDebug {
		if s, ok := m.selectedStatus(); ok {
			e := s.Entry
			debugText := fmt.Sprintf(
				"restartCmd:   %s\nautoRestart:  %v\nmaxRetries:   %d\nfailCount:    %d\nrestartCount: %d\ncooldownSecs: %d\nlastRestart:  %s",
				e.RestartCmd, e.AutoRestart, e.MaxRetries,
				e.FailCount, e.RestartCount, e.CooldownSecs, e.LastRestart,
			)
			panel := styleBorder.
				Width(m.width/3 - 2).
				Render(styleDim.Render("debug\n\n") + debugText)
			content = lipgloss.JoinHorizontal(lipgloss.Top, content, panel)
		}
	}

	return content
}
