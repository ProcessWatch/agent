package process

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
