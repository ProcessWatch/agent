package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	MetricsPort            int    `yaml:"metricsPort"`
	PollIntervalSecs       int    `yaml:"pollIntervalSecs"`
	RestartVerifyDelaySecs int    `yaml:"restartVerifyDelaySecs"`
	LogLevel               string `yaml:"logLevel"`
	DiscordWebhook         string `yaml:"discordWebhook"`
}

var validWebhookPrefixes = []string{
	"https://discord.com/api/webhooks/",
	"https://discordapp.com/api/webhooks/",
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

# Discord webhook for failure alerts (leave empty to disable)
discordWebhook: ""
`
	return os.WriteFile(path, []byte(template), 0644)
}

func defaultConfig() *Config {
	return &Config{
		MetricsPort:            9090,
		PollIntervalSecs:       5,
		RestartVerifyDelaySecs: 3,
		LogLevel:               "info",
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
	if cfg.DiscordWebhook != "" {
		valid := false
		for _, prefix := range validWebhookPrefixes {
			if strings.HasPrefix(cfg.DiscordWebhook, prefix) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid discordWebhook URL: must start with https://discord.com/api/webhooks/ or https://discordapp.com/api/webhooks/")
		}
	}
	return nil
}