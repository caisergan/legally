package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Mode string

type Transport string

const (
	ModeDisabled Mode = "disabled"
	ModeSoftHSM  Mode = "softhsm"
	ModeHardware Mode = "hardware"

	TransportUnix Transport = "unix"
)

type Config struct {
	Enabled           bool
	Environment       string
	Mode              Mode
	Transport         Transport
	SocketPath        string
	StateDir          string
	DatabasePath      string
	ConfigPath        string
	ModuleAlias       string
	ChallengeKeyPath  string
	ChallengeKeyID    string
	SupervisorKeyPath string
	CommandPubKeys    string
	CommandSkew       time.Duration
	MaxArtifactBytes  int64
	AllowedPeerUIDs   string
	PINWindow         time.Duration
	PlanTTL           time.Duration
	AuthorizationTTL  time.Duration
	CapabilityTTL     time.Duration
	ShutdownTimeout   time.Duration
	Version           string
}

func LoadFromEnv() (Config, error) {
	enabled, err := parseBool("SIGNERD_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	shutdownSeconds, err := parsePositiveInt("SIGNERD_SHUTDOWN_TIMEOUT_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}

	pinWindow, err := parseDurationSeconds("SIGNERD_PIN_WINDOW_SECONDS", 90)
	if err != nil {
		return Config{}, err
	}
	planTTL, err := parseDurationSeconds("SIGNERD_PLAN_TTL_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	authorizationTTL, err := parseDurationSeconds("SIGNERD_AUTHORIZATION_TTL_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	capabilityTTL, err := parseDurationSeconds("SIGNERD_CAPABILITY_TTL_SECONDS", 60)
	if err != nil {
		return Config{}, err
	}
	commandSkew, err := parseDurationSeconds("SIGNERD_COMMAND_SKEW_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	maxArtifactBytes, err := parsePositiveInt("SIGNERD_MAX_ARTIFACT_BYTES", 26_214_400)
	if err != nil {
		return Config{}, err
	}

	stateDir := strings.TrimSpace(os.Getenv("SIGNERD_STATE_DIR"))
	databasePath := strings.TrimSpace(os.Getenv("SIGNERD_DATABASE"))
	if databasePath == "" && stateDir != "" {
		databasePath = filepath.Join(stateDir, "signerd.db")
	}

	cfg := Config{
		Enabled:           enabled,
		Environment:       envOrDefault("SIGNERD_ENVIRONMENT", "development"),
		Mode:              Mode(envOrDefault("SIGNERD_MODE", string(ModeDisabled))),
		Transport:         Transport(envOrDefault("SIGNERD_TRANSPORT", string(TransportUnix))),
		SocketPath:        envOrDefault("SIGNERD_SOCKET", "/tmp/yargi-signerd/signerd.sock"),
		StateDir:          stateDir,
		DatabasePath:      databasePath,
		ConfigPath:        strings.TrimSpace(os.Getenv("SIGNERD_CONFIG")),
		ModuleAlias:       strings.TrimSpace(os.Getenv("SIGNERD_MODULE_ALIAS")),
		ChallengeKeyPath:  strings.TrimSpace(os.Getenv("SIGNERD_CHALLENGE_KEY")),
		ChallengeKeyID:    envOrDefault("SIGNERD_CHALLENGE_KEY_ID", "challenge-1"),
		SupervisorKeyPath: strings.TrimSpace(os.Getenv("SIGNERD_SUPERVISOR_KEY")),
		CommandPubKeys:    strings.TrimSpace(os.Getenv("SIGNERD_COMMAND_PUBKEYS")),
		CommandSkew:       commandSkew,
		MaxArtifactBytes:  int64(maxArtifactBytes),
		AllowedPeerUIDs:   strings.TrimSpace(os.Getenv("SIGNERD_ALLOWED_UIDS")),
		PINWindow:         pinWindow,
		PlanTTL:           planTTL,
		AuthorizationTTL:  authorizationTTL,
		CapabilityTTL:     capabilityTTL,
		ShutdownTimeout:   time.Duration(shutdownSeconds) * time.Second,
		Version:           "dev",
	}
	if value := strings.TrimSpace(os.Getenv("SIGNERD_VERSION")); value != "" {
		cfg.Version = value
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	switch c.Environment {
	case "development", "test", "production":
	default:
		return fmt.Errorf("unsupported SIGNERD_ENVIRONMENT %q", c.Environment)
	}

	switch c.Mode {
	case ModeDisabled, ModeSoftHSM, ModeHardware:
	default:
		return fmt.Errorf("unsupported SIGNERD_MODE %q", c.Mode)
	}

	if !c.Enabled {
		if c.Mode != ModeDisabled {
			return errors.New("SIGNERD_MODE must be disabled when SIGNERD_ENABLED is false")
		}
	} else if c.Mode == ModeDisabled {
		return errors.New("SIGNERD_MODE cannot be disabled when SIGNERD_ENABLED is true")
	}

	if c.Environment == "production" && c.Mode == ModeSoftHSM {
		return errors.New("SoftHSM mode is test-only and cannot run in production")
	}

	if c.Transport != TransportUnix {
		return fmt.Errorf("unsupported SIGNERD_TRANSPORT %q: only private Unix sockets are implemented", c.Transport)
	}
	if !filepath.IsAbs(c.SocketPath) {
		return errors.New("SIGNERD_SOCKET must be an absolute path")
	}
	if c.Environment == "production" && pathWithin(c.SocketPath, os.TempDir()) {
		return errors.New("SIGNERD_SOCKET cannot be under the temporary directory in production")
	}

	if c.Enabled {
		if c.StateDir == "" {
			return errors.New("SIGNERD_STATE_DIR is required when the signer is enabled")
		}
		if !filepath.IsAbs(c.StateDir) {
			return errors.New("SIGNERD_STATE_DIR must be an absolute path")
		}
		if !filepath.IsAbs(c.DatabasePath) {
			return errors.New("SIGNERD_DATABASE must be an absolute path")
		}
		if c.Environment == "production" && pathWithin(c.DatabasePath, os.TempDir()) {
			return errors.New("SIGNERD_DATABASE cannot be under the temporary directory in production")
		}
	}
	if c.ConfigPath != "" && !filepath.IsAbs(c.ConfigPath) {
		return errors.New("SIGNERD_CONFIG must be an absolute path")
	}
	if c.ChallengeKeyPath != "" && !filepath.IsAbs(c.ChallengeKeyPath) {
		return errors.New("SIGNERD_CHALLENGE_KEY must be an absolute path")
	}
	if c.SupervisorKeyPath != "" && !filepath.IsAbs(c.SupervisorKeyPath) {
		return errors.New("SIGNERD_SUPERVISOR_KEY must be an absolute path")
	}

	if c.ShutdownTimeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %w", name, err)
	}
	return parsed, nil
}

func parseDurationSeconds(name string, fallbackSeconds int) (time.Duration, error) {
	seconds, err := parsePositiveInt(name, fallbackSeconds)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func parsePositiveInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
