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
}

type Host struct {
	Name                string        `yaml:"name"`
	Connection          string        `yaml:"connection"`
	Endpoint            string        `yaml:"endpoint"`
	Port                int           `yaml:"port"`
	User                string        `yaml:"user"`
	KeyPath             string        `yaml:"key_path"`
	Sudo                bool          `yaml:"sudo"`
	Timeout             time.Duration `yaml:"timeout"`
	ProxyJump           string        `yaml:"proxy_jump"`
	Tags                []string      `yaml:"tags"`
	CollectorPreference string        `yaml:"collector_preference"`
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

	for i := range cfg.Hosts {
		if cfg.Hosts[i].Port == 0 {
			cfg.Hosts[i].Port = 22
		}
		if cfg.Hosts[i].Timeout == 0 {
			cfg.Hosts[i].Timeout = 10 * time.Second
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