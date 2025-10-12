package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"sathub-client/config"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	configPath string
	cfg        *config.Config
	logger     zerolog.Logger
)

var rootCmd = &cobra.Command{
	Use:   "sathub-client",
	Short: "SatHub Data Client for uploading satellite captures",
	Long: `SatHub Data Client automatically monitors directories for new satellite images
and uploads them to your SatHub station. Configuration is loaded from a YAML file.`,
	Example: `  # Run with default config location (~/.config/sathub-client/config.yaml)
  sathub-client

  # Run with custom config file
  sathub-client --config /path/to/config.yaml`,
	PreRun: func(cmd *cobra.Command, args []string) {
		// Load configuration
		var err error
		cfg, err = config.LoadOrDefault(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Validate that token is set
		if cfg.Station.Token == "" {
			fmt.Fprintf(os.Stderr, "Error: station token is not configured\n")
			fmt.Fprintf(os.Stderr, "Please edit your config file at: %s\n", configPath)
			os.Exit(1)
		}

		// Configure logger
		if cfg.Options.Verbose {
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		} else {
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
		}

		// Configure console output
		logger = log.Output(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}).With().
			Str("component", "client").
			Logger()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClient()
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(VERSION)
		logger.Info().Str("version", VERSION).Msg("SatHub Data Client")
	},
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install sathub-client to /usr/bin",
	Long:  "Install sathub-client to /usr/bin. Checks if current version is newer than installed version.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return installBinary()
	},
}

var installServiceCmd = &cobra.Command{
	Use:   "install-service",
	Short: "Install and configure systemd user service",
	Long:  "Install systemd user service for sathub-client and configure station token. Runs as the current user without requiring root privileges.",
	Run: func(cmd *cobra.Command, args []string) {
		if err := installService(); err != nil {
			logger.Fatal().Err(err).Msg("Failed to install service")
		}
	},
}

var uninstallServiceCmd = &cobra.Command{
	Use:   "uninstall-service",
	Short: "Uninstall systemd user service",
	Long:  "Stop and remove the systemd user service for sathub-client. This will stop the service and remove its configuration.",
	Run: func(cmd *cobra.Command, args []string) {
		if err := uninstallService(); err != nil {
			logger.Fatal().Err(err).Msg("Failed to uninstall service")
		}
	},
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update sathub-client to the latest version",
	Long:  "Download and install the latest version of sathub-client from the official source.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return updateClient()
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(installServiceCmd)
	rootCmd.AddCommand(uninstallServiceCmd)
	rootCmd.AddCommand(updateCmd)

	// Only flag is --config for specifying config file location
	rootCmd.Flags().StringVarP(&configPath, "config", "c", config.DefaultConfigPath, "Path to configuration file")
}

func runClient() error {
	logger.Info().
		Str("version", VERSION).
		Str("api_url", cfg.Station.APIURL).
		Str("watch_path", cfg.Paths.Watch).
		Str("processed_dir", cfg.Paths.Processed).
		Msg("Starting SatHub Data Client")

	// Log intervals
	logger.Info().
		Int("health_check_interval", cfg.Intervals.HealthCheck).
		Int("process_delay", cfg.Intervals.ProcessDelay).
		Msg("Configuration parameters")

	// Create configuration for watcher (uses old Config struct)
	watcherConfig := NewConfig(
		cfg.Station.APIURL,
		cfg.Station.Token,
		cfg.Paths.Watch,
		cfg.Paths.Processed,
		time.Duration(cfg.Intervals.ProcessDelay)*time.Second,
	)

	// Create API client
	apiClient := NewAPIClient(cfg.Station.APIURL, cfg.Station.Token, cfg.Options.Insecure)

	// Test API connection with health check
	logger.Info().Msg("Testing API connection...")
	healthResp, err := apiClient.StationHealth()
	if err != nil {
		return fmt.Errorf("initial health check failed: %w", err)
	}

	// Update config with server settings
	watcherConfig.UpdateFromServerSettings(healthResp.Settings)
	logger.Info().Msg("Applied server settings to configuration")

	// Create file watcher
	watcher, err := NewFileWatcher(watcherConfig, apiClient)
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Start the watcher
	if err := watcher.Start(); err != nil {
		return fmt.Errorf("failed to start file watcher: %w", err)
	}

	// Initialize WebSocket client
	wsClient := NewWSClient(cfg, configPath, healthResp.StationID)

	// Periodic health check ticker (may be updated by WebSocket settings)
	ticker := time.NewTicker(time.Duration(cfg.Intervals.HealthCheck) * time.Second)
	defer ticker.Stop()

	// Set up WebSocket callbacks
	wsClient.SetOnSettingsUpdate(func(settings *SettingsUpdatePayload) {
		logger.Info().
			Int("health_check_interval", settings.HealthCheckInterval).
			Int("process_delay", settings.ProcessDelay).
			Msg("Received settings update from server")

		// Update in-memory config
		cfg.Intervals.HealthCheck = settings.HealthCheckInterval
		cfg.Intervals.ProcessDelay = settings.ProcessDelay

		// Update watcher config
		watcherConfig.ProcessDelay = time.Duration(settings.ProcessDelay) * time.Second

		// Save to disk
		if err := cfg.Save(configPath); err != nil {
			logger.Error().Err(err).Msg("Failed to save updated configuration")
		} else {
			logger.Info().Msg("Configuration updated and saved")
		}

		// Reset health check ticker with new interval
		ticker.Reset(time.Duration(settings.HealthCheckInterval) * time.Second)
		logger.Info().Int("interval", settings.HealthCheckInterval).Msg("Health check interval updated")
	})

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Restart signal channel
	restartChan := make(chan struct{})

	wsClient.SetOnRestart(func() {
		logger.Info().Msg("Received restart command from server")
		// Signal the main loop to restart
		select {
		case restartChan <- struct{}{}:
		default:
			logger.Warn().Msg("Restart already in progress")
		}
	})

	// Start WebSocket connection (runs in background with auto-reconnect)
	wsClient.Start()
	defer wsClient.Stop()

	logger.Info().Msg("SatHub Data Client started successfully")

	for {
		select {
		case sig := <-sigChan:
			logger.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
			watcher.Stop()
			return nil

		case <-restartChan:
			logger.Info().Msg("Restart requested, shutting down gracefully...")
			watcher.Stop()
			// Note: When running as systemd service with Restart=always,
			// the service will automatically restart. When running manually,
			// you'll need to restart it yourself.
			return fmt.Errorf("restart requested")

		case <-ticker.C:
			if healthResp, err := apiClient.StationHealth(); err != nil {
				logger.Warn().Err(err).Msg("Health check failed")
			} else {
				// Update config with server settings
				watcherConfig.UpdateFromServerSettings(healthResp.Settings)
				logger.Info().Msg("Health check successful")
			}
		}
	}
}

// installBinary installs the current binary to /usr/bin/sathub-client
func installBinary() error {
	const targetPath = "/usr/bin/sathub-client"

	// Check if we're running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("installation requires root privileges. Please run with sudo")
	}

	// Get current executable path
	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}

	// Check if target already exists and compare versions
	if _, err := os.Stat(targetPath); err == nil {
		// Get installed version
		cmd := exec.Command(targetPath, "version")
		output, err := cmd.Output()
		if err == nil {
			installedVersionStr := strings.TrimSpace(string(output))
			// Extract version from "SatHub Data Client vX.Y.Z"
			re := regexp.MustCompile(`v(\d+\.\d+\.\d+)`)
			matches := re.FindStringSubmatch(installedVersionStr)
			if len(matches) > 1 {
				installedVersion := matches[1]
				if compareVersions(VERSION, installedVersion) <= 0 {
					logger.Info().Str("current_version", VERSION).Str("installed_version", installedVersion).Msg("Current version is not newer than installed version")
					logger.Info().Msg("Installation cancelled.")
					return nil
				}
				logger.Info().Str("from", installedVersion).Str("to", VERSION).Msg("Upgrading")
			}
		}
	}

	logger.Info().Str("version", VERSION).Str("path", targetPath).Msg("Installing sathub-client")

	// Copy current executable to target path
	if err := copyFile(currentExe, targetPath); err != nil {
		return fmt.Errorf("failed to copy binary: %w", err)
	}

	// Make executable
	if err := os.Chmod(targetPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	logger.Info().Msg("Installation completed successfully!")
	logger.Info().Msg("You can now run 'sathub-client' from anywhere.")
	return nil
}

// updateClient downloads and runs the latest installation script
func updateClient() error {
	const installURL = "https://api.sathub.de/install"

	// Check if we're running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("update requires root privileges. Please run with sudo")
	}

	fmt.Printf("Downloading latest version from %s...\n", installURL)
	fmt.Println()

	// Download script to temporary file to preserve stdin for interactive prompts
	tmpFile, err := os.CreateTemp("", "sathub-install-*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Download script
	curlCmd := exec.Command("curl", "-sSL", "-o", tmpFile.Name(), installURL)
	curlCmd.Stdout = os.Stdout
	curlCmd.Stderr = os.Stderr
	if err := curlCmd.Run(); err != nil {
		return fmt.Errorf("failed to download install script: %w", err)
	}

	// Make executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		return fmt.Errorf("failed to make script executable: %w", err)
	}

	// Run script with stdin connected to terminal
	bashCmd := exec.Command("bash", tmpFile.Name())
	bashCmd.Stdout = os.Stdout
	bashCmd.Stderr = os.Stderr
	bashCmd.Stdin = os.Stdin

	if err := bashCmd.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	return nil
}

// installService creates and configures the systemd user service
func installService() error {
	// Get current user's home directory
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	// Use user systemd directory
	systemdUserDir := filepath.Join(currentUser.HomeDir, ".config", "systemd", "user")
	servicePath := filepath.Join(systemdUserDir, "sathub-client.service")
	configFilePath := config.GetConfigPath(config.DefaultConfigPath)

	// Create systemd user directory if it doesn't exist
	if err := os.MkdirAll(systemdUserDir, 0755); err != nil {
		return fmt.Errorf("failed to create systemd user directory: %w", err)
	}

	// Check if binary is installed in /usr/bin or ~/.local/bin
	binaryPath := "/usr/bin/sathub-client"
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		// Try local binary path
		binaryPath = filepath.Join(currentUser.HomeDir, ".local", "bin", "sathub-client")
		if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
			return fmt.Errorf("sathub-client is not installed in /usr/bin or ~/.local/bin")
		}
	}

	// Check if service already exists
	serviceExists := false
	if _, err := os.Stat(servicePath); err == nil {
		serviceExists = true
		fmt.Println("Systemd service already exists.")
	}

	// Load or create config file
	var clientConfig *config.Config
	if _, err := os.Stat(configFilePath); err == nil {
		// Config exists, load it
		clientConfig, err = config.Load(configFilePath)
		if err != nil {
			return fmt.Errorf("failed to load existing config: %w", err)
		}

		fmt.Println("Current configuration:")
		fmt.Printf("  Token: %s\n", maskToken(clientConfig.Station.Token))
		fmt.Printf("  Watch Directory: %s\n", clientConfig.Paths.Watch)
		fmt.Printf("  API URL: %s\n", clientConfig.Station.APIURL)
		fmt.Printf("  Processed Directory: %s\n", clientConfig.Paths.Processed)
		fmt.Println()

		// Ask if user wants to modify configuration
		fmt.Print("Do you want to modify the configuration? (Y/n): ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))

		// If user says no, just reload/restart service if it exists
		if response == "n" || response == "no" {
			fmt.Println("Keeping existing configuration.")

			if serviceExists {
				// Reload systemd user daemon (in case binary was updated)
				if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
					fmt.Printf("Warning: failed to reload systemd: %v\n", err)
				}

				// Try to restart the service
				if err := exec.Command("systemctl", "--user", "restart", "sathub-client").Run(); err != nil {
					fmt.Printf("Warning: failed to restart service: %v\n", err)
					fmt.Println("You may need to run: systemctl --user restart sathub-client")
				} else {
					fmt.Println("Service restarted successfully with updated binary.")
				}
			} else {
				// Create new service with existing config
				if err := createSystemdService(servicePath, binaryPath); err != nil {
					return err
				}
				if err := enableAndStartService(serviceExists); err != nil {
					return err
				}
			}

			fmt.Println("Use 'systemctl --user status sathub-client' to check service status")
			return nil
		}
		fmt.Println()
	} else {
		// No config exists, create default
		clientConfig = config.Default()
	}

	// Get configuration from user
	if err := promptForConfiguration(clientConfig, currentUser.HomeDir); err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	// Save config file
	if err := clientConfig.Save(configFilePath); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}

	fmt.Printf("Configuration saved to: %s\n", configFilePath)

	// Create directories if they don't exist
	for _, dir := range []string{clientConfig.Paths.Watch, clientConfig.Paths.Processed} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Generate and write service file
	if err := createSystemdService(servicePath, binaryPath); err != nil {
		return err
	}

	// Enable and start service
	if err := enableAndStartService(serviceExists); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Service installed and running!")
	fmt.Println("Use 'systemctl --user status sathub-client' to check service status")
	fmt.Println("Use 'journalctl --user -u sathub-client -f' to view logs")
	fmt.Println()
	fmt.Println("To enable the service to start automatically after reboot (even when not logged in):")
	fmt.Println("  loginctl enable-linger $USER")

	return nil
}

// createSystemdService creates the systemd service file
func createSystemdService(servicePath, binaryPath string) error {
	serviceContent := fmt.Sprintf(`[Unit]
Description=SatHub Data Client v2
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`, binaryPath)

	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	return nil
}

// enableAndStartService enables and starts the systemd service
func enableAndStartService(serviceExists bool) error {
	// Reload user systemd
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	// Enable service
	if err := exec.Command("systemctl", "--user", "enable", "sathub-client").Run(); err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}

	// Start or restart the service
	var startCmd *exec.Cmd
	if serviceExists {
		fmt.Println("Restarting service with new configuration...")
		startCmd = exec.Command("systemctl", "--user", "restart", "sathub-client")
	} else {
		fmt.Println("Starting service...")
		startCmd = exec.Command("systemctl", "--user", "start", "sathub-client")
	}

	if err := startCmd.Run(); err != nil {
		fmt.Printf("Warning: Failed to start service: %v\n", err)
		fmt.Println("You can manually start it with: systemctl --user start sathub-client")
	} else {
		fmt.Println("✓ Service started successfully!")
	}

	return nil
}

// promptForConfiguration prompts the user for configuration values
func promptForConfiguration(cfg *config.Config, homeDir string) error {
	reader := bufio.NewReader(os.Stdin)

	// Prompt for token
	if cfg.Station.Token != "" {
		fmt.Printf("Enter station token [%s]: ", maskToken(cfg.Station.Token))
	} else {
		fmt.Print("Enter station token: ")
	}
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)
	if token != "" {
		cfg.Station.Token = token
	}
	if cfg.Station.Token == "" {
		return fmt.Errorf("token cannot be empty")
	}

	// Prompt for watch directory
	fmt.Printf("Enter watch directory [%s]: ", cfg.Paths.Watch)
	watchPath, _ := reader.ReadString('\n')
	watchPath = strings.TrimSpace(watchPath)
	if watchPath != "" {
		cfg.Paths.Watch = watchPath
	}

	// Prompt for API URL
	fmt.Printf("Enter API URL [%s]: ", cfg.Station.APIURL)
	apiURL, _ := reader.ReadString('\n')
	apiURL = strings.TrimSpace(apiURL)
	if apiURL != "" {
		cfg.Station.APIURL = apiURL
	}

	// Prompt for processed directory
	fmt.Printf("Enter processed directory [%s]: ", cfg.Paths.Processed)
	processedDir, _ := reader.ReadString('\n')
	processedDir = strings.TrimSpace(processedDir)
	if processedDir != "" {
		cfg.Paths.Processed = processedDir
	}

	return nil
}

// uninstallService stops and removes the systemd user service
func uninstallService() error {
	// Get current user's home directory
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	// Use user systemd directory
	systemdUserDir := filepath.Join(currentUser.HomeDir, ".config", "systemd", "user")
	servicePath := filepath.Join(systemdUserDir, "sathub-client.service")

	// Check if service exists
	if _, err := os.Stat(servicePath); os.IsNotExist(err) {
		fmt.Println("Service is not installed.")
		return nil
	}

	fmt.Println("Uninstalling sathub-client service...")

	// Stop the service (ignore errors if it's not running)
	fmt.Println("Stopping service...")
	exec.Command("systemctl", "--user", "stop", "sathub-client").Run()

	// Disable the service (ignore errors if it's not enabled)
	fmt.Println("Disabling service...")
	exec.Command("systemctl", "--user", "disable", "sathub-client").Run()

	// Remove the service file
	fmt.Println("Removing service file...")
	if err := os.Remove(servicePath); err != nil {
		return fmt.Errorf("failed to remove service file: %w", err)
	}

	// Reload systemd
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	// Reset failed state if any
	exec.Command("systemctl", "--user", "reset-failed").Run()

	fmt.Println()
	fmt.Println("Service uninstalled successfully!")
	fmt.Println()
	fmt.Println("Note: This does not remove:")
	fmt.Println("  - The sathub-client binary")
	fmt.Println("  - Your data directories")
	fmt.Println("  - Any configuration files")

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// compareVersions compares two version strings (returns -1, 0, 1)
func compareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int

		if i < len(parts1) {
			n1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			n2, _ = strconv.Atoi(parts2[i])
		}

		if n1 < n2 {
			return -1
		} else if n1 > n2 {
			return 1
		}
	}

	return 0
}

// maskToken masks a token for display (shows first 8 and last 4 characters)
func maskToken(token string) string {
	if len(token) <= 12 {
		return strings.Repeat("*", len(token))
	}
	return token[:8] + strings.Repeat("*", len(token)-12) + token[len(token)-4:]
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
