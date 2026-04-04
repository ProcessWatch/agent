package views

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ethan-mdev/process-watch/internal/core"
)

type welcomeChoice int

const (
	choiceLoad welcomeChoice = iota
	choiceClear
)

type WelcomeModel struct {
	ctx       context.Context
	watchlist core.WatchlistManager
	count     int
	choice    welcomeChoice
	width     int
	height    int
}

func NewWelcomeModel(ctx context.Context, watchlist core.WatchlistManager) WelcomeModel {
	count := 0
	if items, err := watchlist.List(ctx); err == nil {
		count = len(items)
	}
	return WelcomeModel{
		ctx:       ctx,
		watchlist: watchlist,
		count:     count,
		choice:    choiceLoad,
	}
}

func (m *WelcomeModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m WelcomeModel) Update(msg tea.Msg) (WelcomeModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.count == 0 {
		if km.String() == "enter" {
			return m, func() tea.Msg { return SwitchToPickerMsg{} }
		}
		if km.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch km.String() {
	case "up", "k":
		m.choice = choiceLoad
	case "down", "j":
		m.choice = choiceClear
	case "enter":
		if m.choice == choiceLoad {
			return m, func() tea.Msg { return SwitchToListMsg{} }
		}
		// Clear all entries then go to picker
		if items, err := m.watchlist.List(m.ctx); err == nil {
			for _, item := range items {
				m.watchlist.Remove(m.ctx, item.Name)
			}
		}
		return m, func() tea.Msg { return SwitchToPickerMsg{} }
	case "q":
		return m, tea.Quit
	}

	return m, nil
}

func (m WelcomeModel) View() string {
	if m.width == 0 {
		return "loading..."
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a78bfa")).Render("ProcessWatch")

	var body string
	if m.count == 0 {
		body = fmt.Sprintf(
			"%s\n\n%s\n\n%s",
			title,
			"No watchlist found.",
			styleDim.Render("Press enter to add your first process · q to quit"),
		)
	} else {
		serviceWord := "service"
		if m.count != 1 {
			serviceWord = "services"
		}

		loadLabel := "  Load existing watchlist"
		clearLabel := "  Clear and start fresh"

		if m.choice == choiceLoad {
			loadLabel = styleBold.Render("> Load existing watchlist")
		}
		if m.choice == choiceClear {
			clearLabel = styleBold.Render("> Clear and start fresh")
		}

		body = fmt.Sprintf(
			"%s\n\nFound %d %s in your watchlist.\n\n%s\n%s\n\n%s",
			title,
			m.count,
			serviceWord,
			loadLabel,
			clearLabel,
			styleDim.Render("↑/↓ to select · enter to confirm · q to quit"),
		)
	}

	// Center it vertically and horizontally
	box := styleBorder.Width(40).Render(body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
