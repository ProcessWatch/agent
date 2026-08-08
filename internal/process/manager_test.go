package process

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ethan-mdev/process-watch/internal/core"
)

func TestSelectorMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    core.MatchMode
		needle  string
		proc    string
		cmdline string
		want    bool
	}{
		// Substring is the legacy default, and its looseness is exactly the
		// problem the other modes exist to solve.
		{"substring hits", core.MatchSubstring, "node", "node.exe", "", true},
		{"substring is case-insensitive", core.MatchSubstring, "node", "NODE", "", true},
		{"substring over-matches", core.MatchSubstring, "node", "node_exporter", "", true},

		// Exact is what stops "node" matching "node_exporter".
		{"exact hits", core.MatchExact, "node", "node", "", true},
		{"exact is case-insensitive", core.MatchExact, "node", "Node", "", true},
		{"exact rejects prefix match", core.MatchExact, "node", "node_exporter", "", false},
		{"exact rejects suffix match", core.MatchExact, "node", "mynode", "", false},

		// Command line is how several identically-named workers are told apart.
		{"cmdline hits", core.MatchCmdline, "gt-web --port=3001", "node", "node gt-web --port=3001", true},
		{"cmdline misses other worker", core.MatchCmdline, "gt-web --port=3001", "node", "node gt-web --port=3002", false},
		{"cmdline ignores process name", core.MatchCmdline, "node", "node", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := selectorMatches(tt.mode, strings.ToLower(tt.needle), tt.proc, tt.cmdline)
			if got != tt.want {
				t.Fatalf("selectorMatches(%s, %q, name=%q, cmdline=%q) = %v, want %v",
					tt.mode, tt.needle, tt.proc, tt.cmdline, got, tt.want)
			}
		})
	}
}

// Unit mode must fail loudly off Linux rather than silently reporting nothing,
// which would look identical to a healthy service being down.
func TestMatchUnitUnsupportedOffLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "linux" {
		t.Skip("unit matching is supported on this platform")
	}

	pm := NewProcessManager(time.Second)
	_, err := pm.Match(context.Background(), core.Selector{Mode: core.MatchUnit, Value: "anything.service"})
	if err == nil {
		t.Fatal("Match() with unit selector returned nil error off Linux, want an error")
	}
	if !strings.Contains(err.Error(), "only supported on Linux") {
		t.Fatalf("error = %q, want it to mention Linux-only support", err)
	}
}

func TestRestartRunsCommand(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	pm := NewProcessManager(30 * time.Second)
	if err := pm.Restart(context.Background(), "true"); err != nil {
		t.Fatalf("Restart() returned error: %v", err)
	}
}

func TestRestartReturnsCommandError(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	pm := NewProcessManager(30 * time.Second)
	err := pm.Restart(context.Background(), "echo boom >&2; exit 3")
	if err == nil {
		t.Fatalf("Restart() expected error for failing command")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Restart() error should include command output, got: %v", err)
	}
}

func TestRestartKillsHungCommand(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	pm := NewProcessManager(1 * time.Second)
	start := time.Now()
	err := pm.Restart(context.Background(), "sleep 30")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("Restart() expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Restart() error = %v, want timeout error", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("Restart() took %s, hung command was not killed promptly", elapsed)
	}
}

func TestRestartDetachedChildDoesNotBlock(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	pm := NewProcessManager(30 * time.Second)
	start := time.Now()
	// The shell exits immediately but the backgrounded sleep inherits the
	// output pipe; WaitDelay must unblock us instead of waiting 20s.
	if err := pm.Restart(context.Background(), "sleep 20 & echo started"); err != nil {
		t.Fatalf("Restart() returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("Restart() took %s, blocked on detached child", elapsed)
	}
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses unix shell commands")
	}
}
