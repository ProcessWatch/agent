package core

import "time"

// EventType represents a lifecycle event in the monitoring system.
type EventType string

const (
	EventProcessDown         EventType = "process_down"
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

// Process represents a system process.
type Process struct {
	Name          string  `json:"name"`
	PID           int32   `json:"pid"`
	State         string  `json:"state"` // e.g., "running", "stopped"
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryMB      float64 `json:"memoryMB"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
}

// WatchlistItem represents a process being monitored.
type WatchlistItem struct {
	Name         string `json:"name"`
	RestartCmd   string `json:"restartCmd"`
	AutoRestart  bool   `json:"autoRestart"`
	MaxRetries   int    `json:"maxRetries"`
	CooldownSecs int    `json:"cooldownSecs"`
	RestartCount int    `json:"restartCount"`
	FailCount    int    `json:"failCount"`
	LastRestart  string `json:"lastRestart"`
}

// WatchStatus (central data type flowing from watcher -> TUI and watcher -> prometheus)
type WatchStatus struct {
	Entry             WatchlistItem `json:"entry"`
	Process           *Process      `json:"process,omitempty"` // nil if not running
	Running           bool          `json:"running"`
	InCooldown        bool          `json:"inCooldown"`
	CooldownRemaining int           `json:"cooldownRemaining"` // seconds
}
