package config

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PollInterval      time.Duration `yaml:"poll_interval"`
	DashboardPassword string        `yaml:"-"`
	Hosts             []Host        `yaml:"hosts"`
	Retry             RetryConfig   `yaml:"retry"`
}

// RetryConfig controls exponential-backoff retries for failed polls.
type RetryConfig struct {
	MaxRetries     int           `yaml:"max_retries"` // attempts per poll (1 = no retry)
	BaseDelay      time.Duration `yaml:"base_delay"`  // first backoff delay
	MaxDelay       time.Duration `yaml:"max_delay"`   // backoff ceiling
	JitterFraction float64       `yaml:"jitter"`      // 0..0.5, fraction of delay
}

func (r RetryConfig) WithDefaults() RetryConfig {
	if r.MaxRetries <= 0 {
		r.MaxRetries = 3
	}
	if r.BaseDelay <= 0 {
		r.BaseDelay = 2 * time.Second
	}
	if r.MaxDelay <= 0 {
		r.MaxDelay = 30 * time.Second
	}
	if r.JitterFraction < 0 || r.JitterFraction > 0.5 {
		r.JitterFraction = 0.2
	}
	return r
}

type Host struct {
	Name                string         `yaml:"name"`
	Connection          string         `yaml:"connection"`
	Endpoint            string         `yaml:"endpoint"`
	Port                int            `yaml:"port"`
	User                string         `yaml:"user"`
	KeyPath             string         `yaml:"key_path"`
	Sudo                bool           `yaml:"sudo"`
	Timeout             time.Duration  `yaml:"timeout"`
	ProxyJump           string         `yaml:"proxy_jump"`
	Tags                []string       `yaml:"tags"`
	CollectorPreference string         `yaml:"collector_preference"`
	RetryMaxRetries     *int64        `yaml:"retry_max_retries"`
	RetryBaseDelay      *time.Duration `yaml:"retry_base_delay"`
	RetryMaxDelay       *time.Duration `yaml:"retry_max_delay"`
	SSHTimeout          *time.Duration `yaml:"ssh_timeout"`        // connection phase; default 10s
	CollectorTimeout    *time.Duration `yaml:"collector_timeout"` // whole-collect budget; default 30s
}

func Load(configDir string) (*Config, error) {
	hostsPath := filepath.Join(configDir, "hosts.yaml")
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.PollInterval == 0 {
		cfg.PollInterval = 30 * time.Second
	}
	cfg.Retry = cfg.Retry.WithDefaults()

	for i := range cfg.Hosts {
		if cfg.Hosts[i].Port == 0 {
			cfg.Hosts[i].Port = 22
		}
		if cfg.Hosts[i].Timeout == 0 {
			cfg.Hosts[i].Timeout = 30 * time.Second // command execution default
		}
	}

	cfg.DashboardPassword = os.Getenv("DASHBOARD_PASSWORD")
	if cfg.DashboardPassword == "" {
		return nil, ErrMissingPassword
	}

	return &cfg, nil
}

var ErrMissingPassword = &configError{"DASHBOARD_PASSWORD environment variable is required"}

type configError struct {
	msg string
}

func (e *configError) Error() string {
	return e.msg
}
