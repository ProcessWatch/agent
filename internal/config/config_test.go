package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCreatesDefaultWhenMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.MetricsPort != 9090 {
		t.Fatalf("MetricsPort = %d, want 9090", cfg.MetricsPort)
	}
	if cfg.PollIntervalSecs != 5 {
		t.Fatalf("PollIntervalSecs = %d, want 5", cfg.PollIntervalSecs)
	}
	if cfg.RestartVerifyDelaySecs != 3 {
		t.Fatalf("RestartVerifyDelaySecs = %d, want 3", cfg.RestartVerifyDelaySecs)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.Alerts.Enabled {
		t.Fatalf("Alerts.Enabled = true, want false")
	}
	if cfg.Alerts.ProjectLabel != "service-watch" {
		t.Fatalf("Alerts.ProjectLabel = %q, want service-watch", cfg.Alerts.ProjectLabel)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected default config file to be created: %v", err)
	}
}

func TestLoadValidConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := strings.Join([]string{
		"metricsPort: 9100",
		"pollIntervalSecs: 7",
		"restartVerifyDelaySecs: 1",
		"logLevel: debug",
		"alerts:",
		"  enabled: true",
		"  discordWebhookURL: https://discord.com/api/webhooks/123/token",
		"  projectLabel: client-acme-prod",
		"",
	}, "\n")

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.MetricsPort != 9100 {
		t.Fatalf("MetricsPort = %d, want 9100", cfg.MetricsPort)
	}
	if cfg.PollIntervalSecs != 7 {
		t.Fatalf("PollIntervalSecs = %d, want 7", cfg.PollIntervalSecs)
	}
	if cfg.RestartVerifyDelaySecs != 1 {
		t.Fatalf("RestartVerifyDelaySecs = %d, want 1", cfg.RestartVerifyDelaySecs)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if !cfg.Alerts.Enabled {
		t.Fatalf("Alerts.Enabled = false, want true")
	}
	if cfg.Alerts.DiscordWebhookURL != "https://discord.com/api/webhooks/123/token" {
		t.Fatalf("Alerts.DiscordWebhookURL = %q, unexpected", cfg.Alerts.DiscordWebhookURL)
	}
	if cfg.Alerts.ProjectLabel != "client-acme-prod" {
		t.Fatalf("Alerts.ProjectLabel = %q, want client-acme-prod", cfg.Alerts.ProjectLabel)
	}
}

func TestLoadValidationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "invalid metricsPort",
			yaml: strings.Join([]string{
				"metricsPort: 0",
				"pollIntervalSecs: 5",
				"restartVerifyDelaySecs: 3",
				"logLevel: info",
				"",
			}, "\n"),
			wantErr: "invalid metricsPort",
		},
		{
			name: "invalid pollIntervalSecs",
			yaml: strings.Join([]string{
				"metricsPort: 9090",
				"pollIntervalSecs: 0",
				"restartVerifyDelaySecs: 3",
				"logLevel: info",
				"",
			}, "\n"),
			wantErr: "invalid pollIntervalSecs",
		},
		{
			name: "invalid restartVerifyDelaySecs",
			yaml: strings.Join([]string{
				"metricsPort: 9090",
				"pollIntervalSecs: 5",
				"restartVerifyDelaySecs: -1",
				"logLevel: info",
				"",
			}, "\n"),
			wantErr: "invalid restartVerifyDelaySecs",
		},
		{
			name: "invalid logLevel",
			yaml: strings.Join([]string{
				"metricsPort: 9090",
				"pollIntervalSecs: 5",
				"restartVerifyDelaySecs: 3",
				"logLevel: trace",
				"",
			}, "\n"),
			wantErr: "invalid logLevel",
		},
		{
			name: "alerts enabled missing webhook url",
			yaml: strings.Join([]string{
				"metricsPort: 9090",
				"pollIntervalSecs: 5",
				"restartVerifyDelaySecs: 3",
				"logLevel: info",
				"alerts:",
				"  enabled: true",
				"  discordWebhookURL: \"\"",
				"  projectLabel: client-ops",
				"",
			}, "\n"),
			wantErr: "invalid alerts.discordWebhookURL",
		},
		{
			name: "alerts enabled invalid webhook url",
			yaml: strings.Join([]string{
				"metricsPort: 9090",
				"pollIntervalSecs: 5",
				"restartVerifyDelaySecs: 3",
				"logLevel: info",
				"alerts:",
				"  enabled: true",
				"  discordWebhookURL: discord-webhook",
				"  projectLabel: client-ops",
				"",
			}, "\n"),
			wantErr: "invalid alerts.discordWebhookURL",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")

			if err := os.WriteFile(path, []byte(tc.yaml), 0644); err != nil {
				t.Fatalf("failed to write config file: %v", err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load() error = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %q, want contains %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLoadSetsDefaultProjectLabelWhenAlertsSectionOmitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := strings.Join([]string{
		"metricsPort: 9090",
		"pollIntervalSecs: 5",
		"restartVerifyDelaySecs: 3",
		"logLevel: info",
		"",
	}, "\n")

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Alerts.ProjectLabel != "service-watch" {
		t.Fatalf("Alerts.ProjectLabel = %q, want service-watch", cfg.Alerts.ProjectLabel)
	}
}
