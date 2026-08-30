package gatewaysync

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the validated runtime configuration for the sync controller.
type Config struct {
	RegistryURL            string
	RegistryTokenFile      string
	TargetNamespace        string
	PodName                string
	PodNamespace           string
	Mode                   Mode
	Prune                  bool
	MinResources           int
	MaxBodyBytes           int64
	PollInterval           time.Duration
	SafetyInterval         time.Duration
	WatchResyncInterval    time.Duration
	AcceptanceTimeout      time.Duration
	AcceptancePollInterval time.Duration
	RegistryRequestTimeout time.Duration
	HTTPAddress            string
	FieldManager           string
	LeaseName              string
	LeaseDuration          time.Duration
	LeaseRenewDeadline     time.Duration
	LeaseRetryPeriod       time.Duration
}

// LoadConfig reads and validates the controller environment.
func LoadConfig() (Config, error) {
	config := Config{
		RegistryURL:       strings.TrimSpace(os.Getenv("REGISTRY_URL")),
		RegistryTokenFile: strings.TrimSpace(os.Getenv("REGISTRY_TOKEN_FILE")),
		TargetNamespace:   strings.TrimSpace(os.Getenv("TARGET_NAMESPACE")),
		PodName:           strings.TrimSpace(os.Getenv("POD_NAME")),
		PodNamespace:      strings.TrimSpace(os.Getenv("POD_NAMESPACE")),
		Mode:              Mode(envDefault("RECONCILIATION_MODE", string(ModeShadow))),
		HTTPAddress:       envDefault("HTTP_ADDRESS", ":9090"),
		FieldManager:      envDefault("FIELD_MANAGER", "agentgateway-registry-sync"),
		LeaseName:         envDefault("LEASE_NAME", "agentgateway-route-sync"),
	}
	for name, value := range map[string]string{
		"REGISTRY_URL":        config.RegistryURL,
		"REGISTRY_TOKEN_FILE": config.RegistryTokenFile,
		"TARGET_NAMESPACE":    config.TargetNamespace,
		"POD_NAME":            config.PodName,
		"POD_NAMESPACE":       config.PodNamespace,
	} {
		if value == "" {
			return Config{}, fmt.Errorf("%s is required", name)
		}
	}
	if config.Mode != ModeActive && config.Mode != ModeShadow && config.Mode != ModeDisabled {
		return Config{}, fmt.Errorf("RECONCILIATION_MODE %q is invalid", config.Mode)
	}

	var err error
	if config.Prune, err = envBool("PRUNE", true); err != nil {
		return Config{}, err
	}
	if config.MinResources, err = envInt("MIN_RESOURCES", 27); err != nil {
		return Config{}, err
	}
	if config.MinResources <= 0 {
		return Config{}, errors.New("MIN_RESOURCES must be positive")
	}
	if config.MaxBodyBytes, err = envInt64("MAX_BODY_BYTES", 5<<20); err != nil {
		return Config{}, err
	}
	if config.MaxBodyBytes <= 0 {
		return Config{}, errors.New("MAX_BODY_BYTES must be positive")
	}

	durations := []struct {
		name        string
		fallback    time.Duration
		destination *time.Duration
	}{
		{name: "POLL_INTERVAL", fallback: 30 * time.Second, destination: &config.PollInterval},
		{name: "SAFETY_INTERVAL", fallback: 5 * time.Minute, destination: &config.SafetyInterval},
		{name: "WATCH_RESYNC_INTERVAL", fallback: 5 * time.Minute, destination: &config.WatchResyncInterval},
		{name: "ACCEPTANCE_TIMEOUT", fallback: 20 * time.Second, destination: &config.AcceptanceTimeout},
		{name: "ACCEPTANCE_POLL_INTERVAL", fallback: 500 * time.Millisecond, destination: &config.AcceptancePollInterval},
		{name: "REGISTRY_REQUEST_TIMEOUT", fallback: 30 * time.Second, destination: &config.RegistryRequestTimeout},
		{name: "LEASE_DURATION", fallback: 15 * time.Second, destination: &config.LeaseDuration},
		{name: "LEASE_RENEW_DEADLINE", fallback: 10 * time.Second, destination: &config.LeaseRenewDeadline},
		{name: "LEASE_RETRY_PERIOD", fallback: 2 * time.Second, destination: &config.LeaseRetryPeriod},
	}
	for _, item := range durations {
		*item.destination, err = envDuration(item.name, item.fallback)
		if err != nil {
			return Config{}, err
		}
		if *item.destination <= 0 {
			return Config{}, fmt.Errorf("%s must be positive", item.name)
		}
	}
	if config.SafetyInterval < config.PollInterval {
		return Config{}, errors.New("SAFETY_INTERVAL must not be shorter than POLL_INTERVAL")
	}
	if !(config.LeaseDuration > config.LeaseRenewDeadline && config.LeaseRenewDeadline > config.LeaseRetryPeriod) {
		return Config{}, errors.New("leader-election durations must satisfy lease > renew > retry")
	}
	return config, nil
}

func envDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func envInt64(name string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}
