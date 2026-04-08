package views

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"io"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleLogError = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleLogDebug = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleLogEvent = lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa"))
)

// --- Log entry list item ---

type logEntry struct {
	Time  string                 `json:"time"`
	Level string                 `json:"level"`
	Event string                 `json:"event"`
	Data  map[string]interface{} `json:"data"`
}

type logItem struct{ entry logEntry }

func (i logItem) FilterValue() string {
	// Filter matches against event name and all data values
	s := i.entry.Event
	for k, v := range i.entry.Data {
		s += " " + k + " " + fmt.Sprintf("%v", v)
	}
	return s
}

// --- Custom delegate ---

type logDelegate struct{}

func (d logDelegate) Height() int                             { return 1 }
func (d logDelegate) Spacing() int                            { return 0 }
func (d logDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d logDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	li, ok := item.(logItem)
	if !ok {
		return
	}
	e := li.entry

	t := ""
	if len(e.Time) >= 19 {
		t = styleDim.Render(e.Time[11:19])
	}

	var levelStyle lipgloss.Style
	switch e.Level {
	case "ERROR":
		levelStyle = styleLogError
	case "DEBUG":
		levelStyle = styleLogDebug
	default:
		levelStyle = styleDim
	}
	level := levelStyle.Render(fmt.Sprintf("%-5s", e.Level))
	event := styleLogEvent.Render(e.Event)

	keys := sortDataKeys(e.Data)
	var data string
	for _, k := range keys {
		if data != "" {
			data += "  "
		}
		data += fmt.Sprintf("%s=%v", styleDim.Render(k), e.Data[k])
	}

	if data != "" {
		fmt.Fprintf(w, "  %s  %s  %s  %s", t, level, event, data)
	} else {
		fmt.Fprintf(w, "  %s  %s  %s", t, level, event)
	}
}

// --- LogsModel ---

type LogsModel struct {
	list    list.Model
	logPath string
	width   int
	height  int
}

func NewLogsModel(logPath string) LogsModel {
	l := list.New([]list.Item{}, logDelegate{}, 0, 0)
	l.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a78bfa"))
	l.Title = "ProcessWatch  event log"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ShowFullHelp = key.NewBinding()
	l.KeyMap.CloseFullHelp = key.NewBinding()
	l.Filter = func(term string, targets []string) []list.Rank {
		var ranks []list.Rank
		for i, t := range targets {
			if strings.Contains(strings.ToLower(t), strings.ToLower(term)) {
				ranks = append(ranks, list.Rank{Index: i})
			}
		}
		return ranks
	}
	return LogsModel{
		logPath: logPath,
		list:    l,
	}
}

func (m *LogsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h)
}

func (m *LogsModel) Load() {
	f, err := os.Open(m.logPath)
	if err != nil {
		m.list.SetItems([]list.Item{})
		m.list.Title = "ProcessWatch  event log  " + styleLogError.Render("could not open log file: "+err.Error())
		return
	}
	defer f.Close()

	var items []list.Item
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry logEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		items = append(items, logItem{entry: entry})
	}

	m.list.SetItems(items)
	// Start scrolled to the bottom so the most recent entries are visible
	m.list.Select(len(items) - 1)
}

func (m LogsModel) Update(msg tea.Msg) (LogsModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		// Only intercept q when not actively filtering
		if km.String() == "q" && !m.list.SettingFilter() {
			return m, func() tea.Msg { return SwitchToListMsg{} }
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m LogsModel) View() string {
	if m.width == 0 {
		return "loading..."
	}
	return m.list.View()
}

var keyOrder = []string{"name", "pid", "cpuPercent", "memoryMB"}

func sortDataKeys(data map[string]interface{}) []string {
	keys := make([]string, 0, len(data))
	seen := make(map[string]bool)

	for _, k := range keyOrder {
		if _, ok := data[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	for k := range data {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	return keys
}
