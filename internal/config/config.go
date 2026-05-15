// Package config handles loading and validating the nzbgrab configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the application configuration.
type Config struct {
	Server   ServerConfig   `toml:"server"`
	Download DownloadConfig `toml:"download"`
}

// ServerConfig contains NNTP server settings.
type ServerConfig struct {
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	Username    string `toml:"username"`
	Password    string `toml:"password"`
	Connections int    `toml:"connections"`
}

// DownloadConfig contains download behavior settings.
type DownloadConfig struct {
	Dir      string `toml:"dir"`
	Parallel int    `toml:"parallel"`
}

// UseSSL returns true if the port implies SSL (563).
func (s *ServerConfig) UseSSL() bool {
	return s.Port == 563
}

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "nzbgrab", "config.toml")
}

// Load reads and parses the config file from the given path.
func Load(path string) (*Config, error) {
	// Expand ~ in path
	path = expandHome(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Apply defaults
	cfg.applyDefaults()

	// Validate
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	// Expand ~ in download dir
	cfg.Download.Dir = expandHome(cfg.Download.Dir)

	return &cfg, nil
}

// applyDefaults sets default values for unspecified fields.
func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 563 // Default to SSL
	}
	if c.Server.Connections == 0 {
		c.Server.Connections = 10
	}
	if c.Download.Dir == "" {
		home, _ := os.UserHomeDir()
		c.Download.Dir = filepath.Join(home, "Downloads")
	}
	if c.Download.Parallel == 0 {
		c.Download.Parallel = 2
	}
}

// validate checks that required fields are present and values are sensible.
func (c *Config) validate() error {
	if c.Server.Host == "" {
		return fmt.Errorf("server.host is required")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if c.Server.Connections < 1 || c.Server.Connections > 100 {
		return fmt.Errorf("server.connections must be between 1 and 100")
	}
	if c.Download.Parallel < 1 || c.Download.Parallel > 10 {
		return fmt.Errorf("download.parallel must be between 1 and 10")
	}
	return nil
}

// expandHome replaces ~ with the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
