package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	// Create a temp config file
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	content := `
[server]
host = "news.example.com"
port = 563
username = "user"
password = "pass"
connections = 5

[download]
dir = "/tmp/downloads"
parallel = 2
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.Host != "news.example.com" {
		t.Errorf("Host = %q, want %q", cfg.Server.Host, "news.example.com")
	}
	if cfg.Server.Port != 563 {
		t.Errorf("Port = %d, want %d", cfg.Server.Port, 563)
	}
	if !cfg.Server.UseSSL() {
		t.Error("UseSSL() = false, want true for port 563")
	}
	if cfg.Server.Connections != 5 {
		t.Errorf("Connections = %d, want %d", cfg.Server.Connections, 5)
	}
	if cfg.Download.Parallel != 2 {
		t.Errorf("Parallel = %d, want %d", cfg.Download.Parallel, 2)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// Minimal config - only required field
	content := `
[server]
host = "news.example.com"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Check defaults applied
	if cfg.Server.Port != 563 {
		t.Errorf("Port default = %d, want 563", cfg.Server.Port)
	}
	if cfg.Server.Connections != 10 {
		t.Errorf("Connections default = %d, want 10", cfg.Server.Connections)
	}
	if cfg.Download.Parallel != 2 {
		t.Errorf("Parallel default = %d, want 2", cfg.Download.Parallel)
	}
}

func TestLoadValidation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// Missing host
	content := `
[server]
port = 563
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Error("Load() with missing host should fail")
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	
	tests := []struct {
		input string
		want  string
	}{
		{"~/Downloads", filepath.Join(home, "Downloads")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := expandHome(tt.input)
		if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
