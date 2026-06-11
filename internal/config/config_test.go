package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return path
}

func TestLoadMissingFileWritesDefault(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.PollIntervalSecs != 5 || cfg.RestartTimeoutSecs != 60 || cfg.LogLevel != "info" {
		t.Fatalf("Load() defaults mismatch: %+v", cfg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Load() did not write default config: %v", err)
	}
}

func TestLoadPartialConfigAppliesDefaults(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "reporting:\n  enabled: false\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error for partial config: %v", err)
	}
	if cfg.PollIntervalSecs != 5 {
		t.Fatalf("PollIntervalSecs = %d, want default 5", cfg.PollIntervalSecs)
	}
	if cfg.RestartTimeoutSecs != 60 {
		t.Fatalf("RestartTimeoutSecs = %d, want default 60", cfg.RestartTimeoutSecs)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want default \"info\"", cfg.LogLevel)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"negative poll interval", "pollIntervalSecs: -1\n", "pollIntervalSecs"},
		{"negative verify delay", "restartVerifyDelaySecs: -1\n", "restartVerifyDelaySecs"},
		{"negative restart timeout", "restartTimeoutSecs: -5\n", "restartTimeoutSecs"},
		{"bad log level", "logLevel: verbose\n", "logLevel"},
		{"reporting without key", "reporting:\n  enabled: true\n", "apiKey"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, tc.content)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %v, want error mentioning %q", err, tc.wantErr)
			}
		})
	}
}
