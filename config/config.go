package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the client configuration
type Config struct {
	Station   StationConfig   `yaml:"station"`
	Paths     PathsConfig     `yaml:"paths"`
	Intervals IntervalsConfig `yaml:"intervals"`
	Options   OptionsConfig   `yaml:"options"`
}

// StationConfig holds station-specific configuration
type StationConfig struct {
	Token  string `yaml:"token"`
	APIURL string `yaml:"api_url"`
}

// PathsConfig holds directory paths
type PathsConfig struct {
	Watch     string `yaml:"watch"`
	Processed string `yaml:"processed"`
}

// IntervalsConfig holds timing configurations
type IntervalsConfig struct {
	HealthCheck  int `yaml:"health_check"`  // seconds
	ProcessDelay int `yaml:"process_delay"` // seconds
}

// OptionsConfig holds optional settings
type OptionsConfig struct {
	Insecure bool `yaml:"insecure"`
	Verbose  bool `yaml:"verbose"`
}

// Load reads the configuration from a YAML file
func Load(path string) (*Config, error) {
	// Expand tilde in path
	path = expandPath(path)

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate required fields
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// Save writes the configuration to a YAML file
func (c *Config) Save(path string) error {
	// Expand tilde in path
	path = expandPath(path)

	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal to YAML
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write file
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Station.Token == "" {
		return fmt.Errorf("station token is required")
	}
	if c.Station.APIURL == "" {
		return fmt.Errorf("api_url is required")
	}
	if c.Paths.Watch == "" {
		return fmt.Errorf("watch path is required")
	}
	if c.Paths.Processed == "" {
		return fmt.Errorf("processed path is required")
	}
	if c.Intervals.HealthCheck <= 0 {
		return fmt.Errorf("health_check interval must be positive")
	}
	if c.Intervals.ProcessDelay <= 0 {
		return fmt.Errorf("process_delay must be positive")
	}
	return nil
}

// Default returns a configuration with default values
func Default() *Config {
	homeDir, _ := os.UserHomeDir()

	return &Config{
		Station: StationConfig{
			Token:  "",
			APIURL: DefaultAPIURL,
		},
		Paths: PathsConfig{
			Watch:     filepath.Join(homeDir, "sathub", "data"),
			Processed: filepath.Join(homeDir, "sathub", "processed"),
		},
		Intervals: IntervalsConfig{
			HealthCheck:  DefaultHealthCheckInterval,
			ProcessDelay: DefaultProcessDelay,
		},
		Options: OptionsConfig{
			Insecure: false,
			Verbose:  false,
		},
	}
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(homeDir, path[2:])
	}
	return path
}

// LoadOrDefault loads config from path, or returns default if file doesn't exist
func LoadOrDefault(path string) (*Config, error) {
	expandedPath := expandPath(path)

	// Check if file exists
	if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
		// Return default config
		config := Default()

		// Try to save it for next time
		if err := config.Save(expandedPath); err != nil {
			// Not fatal, just log
			fmt.Fprintf(os.Stderr, "Warning: Could not create default config file: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Created default config file at: %s\n", expandedPath)
			fmt.Fprintf(os.Stderr, "Please edit this file and set your station token.\n")
		}

		return config, nil
	}

	// Load existing config
	return Load(path)
}

// GetConfigPath returns the expanded config path
func GetConfigPath(path string) string {
	return expandPath(path)
}
