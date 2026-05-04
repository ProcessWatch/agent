package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ReportingConfig struct {
	Enabled bool   `yaml:"enabled"`
	APIKey  string `yaml:"apiKey"`
}

type Config struct {
	PollIntervalSecs       int             `yaml:"pollIntervalSecs"`
	RestartVerifyDelaySecs int             `yaml:"restartVerifyDelaySecs"`
	LogLevel               string          `yaml:"logLevel"`
	Reporting              ReportingConfig `yaml:"reporting"`
}

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

func WriteDefault(path string) error {
	const template = `# ProcessWatch configuration
pollIntervalSecs: 5
restartVerifyDelaySecs: 3   # seconds to wait after restart before checking health
logLevel: info              # info | debug

# reporting: configure to send metrics to a ProcessWatch dashboard
# reporting:
#   enabled: true
#   apiKey: "pw_live_..."
`
	return os.WriteFile(path, []byte(template), 0644)
}

func defaultConfig() *Config {
	return &Config{
		PollIntervalSecs:       5,
		RestartVerifyDelaySecs: 3,
		LogLevel:               "info",
		Reporting: ReportingConfig{
			Enabled: false,
		},
	}
}

func applyDefaults(_ *Config) {}

func validate(cfg *Config) error {
	if cfg.PollIntervalSecs < 1 {
		return fmt.Errorf("invalid pollIntervalSecs %d: must be >= 1", cfg.PollIntervalSecs)
	}
	if cfg.RestartVerifyDelaySecs < 0 {
		return fmt.Errorf("invalid restartVerifyDelaySecs %d: must be >= 0", cfg.RestartVerifyDelaySecs)
	}
	if cfg.LogLevel != "info" && cfg.LogLevel != "debug" {
		return fmt.Errorf("invalid logLevel %q: must be \"info\" or \"debug\"", cfg.LogLevel)
	}
	if cfg.Reporting.Enabled {
		if cfg.Reporting.APIKey == "" {
			return fmt.Errorf("invalid reporting.apiKey: must be set when reporting.enabled is true")
		}
	}
	return nil
}
