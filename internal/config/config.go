package config

import (
	"fmt"
	"net/url"
	"os"

	"gopkg.in/yaml.v3"
)

type AlertsConfig struct {
	Enabled           bool   `yaml:"enabled"`
	DiscordWebhookURL string `yaml:"discordWebhookURL"`
	ProjectLabel      string `yaml:"projectLabel"`
}

type Config struct {
	MetricsPort            int          `yaml:"metricsPort"`
	PollIntervalSecs       int          `yaml:"pollIntervalSecs"`
	RestartVerifyDelaySecs int          `yaml:"restartVerifyDelaySecs"`
	LogLevel               string       `yaml:"logLevel"`
	Alerts                 AlertsConfig `yaml:"alerts"`
}

// Load reads config from path. If the file does not exist, it writes a default
// config to that path and returns it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := defaultConfig()
		if writeErr := WriteDefault(path); writeErr != nil {
			return nil, fmt.Errorf("config file not found and could not write default: %w", writeErr)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// WriteDefault writes a commented default config.yaml to path.
func WriteDefault(path string) error {
	const template = `# ServiceWatch configuration
metricsPort: 9090
pollIntervalSecs: 5
restartVerifyDelaySecs: 3   # seconds to wait after restart before checking health
logLevel: info              # info | debug
alerts:
	enabled: false
	discordWebhookURL: ""     # ex: https://discord.com/api/webhooks/<id>/<token>
	projectLabel: "service-watch"

`
	return os.WriteFile(path, []byte(template), 0644)
}

func defaultConfig() *Config {
	return &Config{
		MetricsPort:            9090,
		PollIntervalSecs:       5,
		RestartVerifyDelaySecs: 3,
		LogLevel:               "info",
		Alerts: AlertsConfig{
			Enabled:           false,
			DiscordWebhookURL: "",
			ProjectLabel:      "service-watch",
		},
	}
}

func applyDefaults(cfg *Config) {
	if cfg.Alerts.ProjectLabel == "" {
		cfg.Alerts.ProjectLabel = "service-watch"
	}
}

func validate(cfg *Config) error {
	if cfg.MetricsPort < 1 || cfg.MetricsPort > 65535 {
		return fmt.Errorf("invalid metricsPort %d: must be between 1 and 65535", cfg.MetricsPort)
	}
	if cfg.PollIntervalSecs < 1 {
		return fmt.Errorf("invalid pollIntervalSecs %d: must be >= 1", cfg.PollIntervalSecs)
	}
	if cfg.RestartVerifyDelaySecs < 0 {
		return fmt.Errorf("invalid restartVerifyDelaySecs %d: must be >= 0", cfg.RestartVerifyDelaySecs)
	}
	if cfg.LogLevel != "info" && cfg.LogLevel != "debug" {
		return fmt.Errorf("invalid logLevel %q: must be \"info\" or \"debug\"", cfg.LogLevel)
	}
	if cfg.Alerts.Enabled {
		if cfg.Alerts.DiscordWebhookURL == "" {
			return fmt.Errorf("invalid alerts.discordWebhookURL: must be set when alerts.enabled is true")
		}
		u, err := url.Parse(cfg.Alerts.DiscordWebhookURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid alerts.discordWebhookURL: must be a valid http/https URL")
		}
	}
	return nil
}
