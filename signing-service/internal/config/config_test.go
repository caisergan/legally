package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDisabledDefaultsAreSafe(t *testing.T) {
	cfg := Config{
		Enabled:         false,
		Environment:     "development",
		Mode:            ModeDisabled,
		Transport:       TransportUnix,
		SocketPath:      filepath.Join(t.TempDir(), "signerd.sock"),
		ShutdownTimeout: time.Second,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("safe disabled configuration rejected: %v", err)
	}
}

func TestEnabledServiceRequiresExplicitMode(t *testing.T) {
	cfg := Config{
		Enabled:         true,
		Environment:     "development",
		Mode:            ModeDisabled,
		Transport:       TransportUnix,
		SocketPath:      filepath.Join(t.TempDir(), "signerd.sock"),
		ShutdownTimeout: time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled service accepted disabled mode")
	}
}

func TestSoftHSMCannotRunInProduction(t *testing.T) {
	cfg := Config{
		Enabled:         true,
		Environment:     "production",
		Mode:            ModeSoftHSM,
		Transport:       TransportUnix,
		SocketPath:      "/run/yargi-signerd/signerd.sock",
		ShutdownTimeout: time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("production configuration accepted SoftHSM")
	}
}

func TestNetworkTransportFailsClosed(t *testing.T) {
	cfg := Config{
		Enabled:         true,
		Environment:     "development",
		Mode:            ModeSoftHSM,
		Transport:       "https",
		SocketPath:      filepath.Join(t.TempDir(), "signerd.sock"),
		ShutdownTimeout: time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("unimplemented network transport was accepted")
	}
}

func enabledConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Enabled:         true,
		Environment:     "development",
		Mode:            ModeSoftHSM,
		Transport:       TransportUnix,
		SocketPath:      filepath.Join(dir, "signerd.sock"),
		StateDir:        filepath.Join(dir, "state"),
		DatabasePath:    filepath.Join(dir, "state", "signerd.db"),
		ShutdownTimeout: time.Second,
	}
}

func TestEnabledServiceRequiresStateDirectory(t *testing.T) {
	cfg := enabledConfig(t)
	cfg.StateDir = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled service accepted an empty state directory")
	}
}

func TestEnabledServiceRequiresAbsolutePaths(t *testing.T) {
	cfg := enabledConfig(t)
	cfg.StateDir = "relative/state"
	cfg.DatabasePath = "relative/state/signerd.db"
	if err := cfg.Validate(); err == nil {
		t.Fatal("relative state paths were accepted")
	}
}

func TestEnabledServiceAcceptsPrivatePaths(t *testing.T) {
	if err := enabledConfig(t).Validate(); err != nil {
		t.Fatalf("valid enabled configuration rejected: %v", err)
	}
}

func TestConfigPathMustBeAbsolute(t *testing.T) {
	cfg := enabledConfig(t)
	cfg.ConfigPath = "relative/modules.yaml"
	if err := cfg.Validate(); err == nil {
		t.Fatal("relative config path was accepted")
	}
}
