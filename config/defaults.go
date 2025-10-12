package config

const (
	// DefaultAPIURL is the default SatHub API endpoint
	DefaultAPIURL = "https://api.sathub.de"

	// DefaultHealthCheckInterval is the default health check interval in seconds
	DefaultHealthCheckInterval = 300

	// DefaultProcessDelay is the default delay before processing new directories in seconds
	DefaultProcessDelay = 60

	// DefaultConfigPath is the default location for the config file
	DefaultConfigPath = "~/.config/sathub-client/config.yaml"
)
