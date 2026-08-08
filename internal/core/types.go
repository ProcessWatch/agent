// Package core defines the data types and interfaces shared by the watcher,
// storage, TUI, and reporter. It depends on nothing else in the project, so
// any package can import it without cycles.
package core

import "time"

// EventType represents a lifecycle event in the monitoring system.
type EventType string

const (
	EventProcessDown EventType = "process_down"
	// EventProcessRecovered is emitted whenever a watched process is observed
	// running after previously being down — regardless of what restarted it.
	// Without it, a process recovered by systemd (or by hand) leaves its
	// dashboard incident open forever, because the only other resolving
	// events come from the agent's own restart path.
	EventProcessRecovered    EventType = "process_recovered"
	EventRestartAttempt      EventType = "restart_attempt"
	EventRestartFailed       EventType = "restart_failed"
	EventRestartVerifyFailed EventType = "restart_verify_failed"
	EventRestartSuccess      EventType = "restart_success"
	EventMaxRetriesExceeded  EventType = "max_retries_exceeded"
)

// ReportEvent represents a discrete state transition that occurred during a poll cycle.
type ReportEvent struct {
	Time    time.Time `json:"time"`
	Type    EventType `json:"type"`
	Process string    `json:"process"`
}

// MatchMode determines how a watchlist entry is matched against the system.
type MatchMode string

const (
	// MatchSubstring is the historical behaviour: a case-insensitive substring
	// match on the process name. Kept as the default so watchlists written by
	// older versions keep working, but it cannot distinguish several processes
	// that share a name — prefer one of the modes below.
	MatchSubstring MatchMode = "substring"
	// MatchExact matches the process name exactly, case-insensitively.
	MatchExact MatchMode = "exact"
	// MatchCmdline matches a substring of the full command line, which is how
	// you tell apart several workers that are all called "node".
	MatchCmdline MatchMode = "cmdline"
	// MatchUnit asks systemd about a unit (or a glob such as "gt-web@*") and
	// is therefore Linux-only. It reports what the service manager believes,
	// rather than guessing from the process table.
	MatchUnit MatchMode = "unit"
)

// Selector describes what a watchlist entry actually watches.
type Selector struct {
	Mode  MatchMode `json:"mode"`
	Value string    `json:"value"`
}

func (s Selector) String() string {
	switch s.Mode {
	case MatchUnit:
		return "systemd unit: " + s.Value
	case MatchExact:
		return "program: " + s.Value
	case MatchCmdline:
		return "command line: " + s.Value
	default:
		return "name contains: " + s.Value
	}
}

// Process represents a system process.
//
// Cmdline is populated only when a selector needs it. It is deliberately never
// reported to the dashboard: command lines routinely carry API keys, database
// URLs and tokens passed as flags.
type Process struct {
	Name          string  `json:"name"`
	PID           int32   `json:"pid"`
	State         string  `json:"state"` // e.g., "running", "stopped"
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryMB      float64 `json:"memoryMB"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
	Cmdline       string  `json:"-"`
}

// WatchlistItem represents a process being monitored.
type WatchlistItem struct {
	Name          string    `json:"name"`
	MatchMode     MatchMode `json:"matchMode,omitempty"`
	Selector      string    `json:"selector,omitempty"`
	ExpectedCount int       `json:"expectedCount,omitempty"`
	RestartCmd    string    `json:"restartCmd"`
	AutoRestart   bool      `json:"autoRestart"`
	MaxRetries    int       `json:"maxRetries"` // 0 = retry forever
	CooldownSecs  int       `json:"cooldownSecs"`
	RestartCount  int       `json:"restartCount"`
	FailCount     int       `json:"failCount"`
	LastRestart   string    `json:"lastRestart"` // RFC3339 time of the last restart attempt (success or failure)
}

// ResolvedSelector fills in the defaults an older watchlist.json omits, so
// entries written before match modes existed keep behaving exactly as before.
func (w WatchlistItem) ResolvedSelector() Selector {
	mode := w.MatchMode
	if mode == "" {
		mode = MatchSubstring
	}
	value := w.Selector
	if value == "" {
		value = w.Name
	}
	return Selector{Mode: mode, Value: value}
}

// Expected returns how many instances should be running. Zero and negative
// values mean "at least one", which is what every pre-existing entry implies.
func (w WatchlistItem) Expected() int {
	if w.ExpectedCount < 1 {
		return 1
	}
	return w.ExpectedCount
}

// WatchStatus is the central data type flowing from the watcher to the TUI and reporter.
type WatchStatus struct {
	Entry             WatchlistItem `json:"entry"`
	Process           *Process      `json:"process,omitempty"` // representative match; nil if none running
	Running           bool          `json:"running"`
	Found             int           `json:"found"`    // instances currently matching the selector
	Expected          int           `json:"expected"` // instances that should be running
	InCooldown        bool          `json:"inCooldown"`
	CooldownRemaining int           `json:"cooldownRemaining"` // seconds
}

// Degraded reports whether the entry is running but not at full strength —
// three workers where four are expected, or a duplicate that should not exist.
func (s WatchStatus) Degraded() bool {
	return s.Running && s.Expected > 0 && s.Found != s.Expected
}
