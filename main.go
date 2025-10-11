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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	token               string
	watchPath           string
	apiURL              string
	processedDir        string
	processDelay        int
	healthCheckInterval int
	verbose             bool
	insecure            bool
	logger              zerolog.Logger
)

var rootCmd = &cobra.Command{
	Use:   "sathub-client",
	Short: "SatHub Data Client for uploading satellite captures",
	Long: `SatHub Data Client automatically monitors directories for new satellite images
and uploads them to your SatHub station using the station's API token.`,
	Example: `  # Basic usage
  sathub-client --token abc123def --watch /home/user/satellite-images

  # With custom API URL and processed directory
  sathub-client --token abc123def --watch /path/to/images --api https://my-api.com --processed ./done`,
	PreRun: func(cmd *cobra.Command, args []string) {
		// Configure logger
		if verbose {
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

	// Set defaults from environment variables
	defaultToken := os.Getenv("STATION_TOKEN")
	defaultWatch := os.Getenv("WATCH_PATHS")
	defaultAPI := getEnvWithDefault("API_URL", "https://api.sathub.de")
	defaultProcessed := getEnvWithDefault("PROCESSED_DIR", "./processed")
	defaultHealthCheckInterval := getEnvWithDefault("HEALTH_CHECK_INTERVAL", "300")
	defaultProcessDelay := getEnvWithDefault("PROCESS_DELAY", "60")

	// Parse int values from environment
	healthInterval, err := strconv.Atoi(defaultHealthCheckInterval)
	if err != nil || healthInterval <= 0 {
		healthInterval = 300
	}
	procDelay, err := strconv.Atoi(defaultProcessDelay)
	if err != nil || procDelay <= 0 {
		procDelay = 60
	}

	// Define command line flags
	rootCmd.Flags().StringVarP(&token, "token", "t", defaultToken, "Station API token (required, or set STATION_TOKEN env var)")
	rootCmd.Flags().StringVarP(&watchPath, "watch", "w", defaultWatch, "Directory path to watch for new images (required, or set WATCH_PATHS env var)")
	rootCmd.Flags().StringVarP(&apiURL, "api", "a", defaultAPI, "SatHub API URL (or set API_URL env var)")
	rootCmd.Flags().StringVarP(&processedDir, "processed", "p", defaultProcessed, "Directory to move processed files (or set PROCESSED_DIR env var)")
	rootCmd.Flags().IntVar(&processDelay, "process-delay", procDelay, "Delay in seconds before processing new directories (or set PROCESS_DELAY env var)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.Flags().BoolVarP(&insecure, "insecure", "k", false, "Skip TLS certificate verification (for self-signed certificates)")
	rootCmd.Flags().IntVar(&healthCheckInterval, "health-interval", healthInterval, "Health check interval in seconds (or set HEALTH_CHECK_INTERVAL env var)")

	// Only mark as required if not set via environment
	if defaultToken == "" {
		rootCmd.MarkFlagRequired("token")
	}
	if defaultWatch == "" {
		rootCmd.MarkFlagRequired("watch")
	}
}

func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func runClient() error {
	logger.Info().
		Str("version", VERSION).
		Str("api_url", apiURL).
		Str("watch_path", watchPath).
		Str("processed_dir", processedDir).
		Msg("Starting SatHub Data Client")

	// Log intervals
	logger.Info().
		Int("health_check_interval", healthCheckInterval).
		Int("process_delay", processDelay).
		Msg("Configuration parameters")

	// Create configuration from command line arguments
	config := NewConfig(apiURL, token, watchPath, processedDir, time.Duration(processDelay)*time.Second)

	// Create API client
	apiClient := NewAPIClient(config.APIURL, config.StationToken, insecure)

	// Test API connection with health check
	logger.Info().Msg("Testing API connection...")
	if err := apiClient.StationHealth(); err != nil {
		logger.Warn().Err(err).Msg("Initial health check failed")
	} else {
		logger.Info().Msg("API connection successful")
	}

	// Create file watcher
	watcher, err := NewFileWatcher(config, apiClient)
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Start the watcher
	if err := watcher.Start(); err != nil {
		return fmt.Errorf("failed to start file watcher: %w", err)
	}

	logger.Info().Msg("SatHub Data Client started successfully")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Periodic health check
	ticker := time.NewTicker(time.Duration(healthCheckInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case sig := <-sigChan:
			logger.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
			watcher.Stop()
			return nil

		case <-ticker.C:
			if err := apiClient.StationHealth(); err != nil {
				logger.Warn().Err(err).Msg("Health check failed")
			} else {
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

	// Check if service already exists and extract existing configuration
	var existingConfig *ServiceConfig
	serviceExists := false
	if _, err := os.Stat(servicePath); err == nil {
		serviceExists = true
		fmt.Println("Systemd service already exists.")

		// Extract existing configuration from service file
		existingConfig = extractServiceConfiguration(servicePath)
		if existingConfig != nil {
			fmt.Println("Current configuration:")
			fmt.Printf("  Token: %s\n", maskToken(existingConfig.Token))
			fmt.Printf("  Watch Directory: %s\n", existingConfig.WatchPath)
			fmt.Printf("  API URL: %s\n", existingConfig.APIURL)
			fmt.Printf("  Processed Directory: %s\n", existingConfig.ProcessedDir)
			fmt.Println()

			// Ask if user wants to modify configuration
			fmt.Print("Do you want to modify the configuration? (Y/n): ")
			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.ToLower(strings.TrimSpace(response))

			// If user says no (or enters 'n'), keep existing config and skip to reload
			if response == "n" || response == "no" {
				fmt.Println("Keeping existing configuration.")

				// Reload systemd (in case binary was updated)
				if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
					return fmt.Errorf("failed to reload systemd: %w", err)
				}

				fmt.Println("Service configuration unchanged.")
				fmt.Println("Use 'sudo systemctl restart sathub-client' to restart the service")
				fmt.Println("Use 'sudo systemctl status sathub-client' to check service status")
				return nil
			}
			fmt.Println()
		}
	}

	// Get configuration from user (with existing values as defaults)
	config, err := getServiceConfiguration(existingConfig, currentUser.HomeDir)
	if err != nil {
		return fmt.Errorf("failed to get service configuration: %w", err)
	}

	// Generate service content
	serviceContent := generateServiceFile(config, binaryPath)

	// Write service file
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	// Create directories if they don't exist
	for _, dir := range []string{config.WatchPath, config.ProcessedDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Reload user systemd and enable service
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

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

	fmt.Println()
	fmt.Println("Service installed and running!")
	fmt.Println("Use 'systemctl --user status sathub-client' to check service status")
	fmt.Println("Use 'journalctl --user -u sathub-client -f' to view logs")
	fmt.Println()
	fmt.Println("To enable the service to start automatically after reboot (even when not logged in):")
	fmt.Println("  loginctl enable-linger $USER")

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

// extractServiceConfiguration extracts configuration from an existing service file
func extractServiceConfiguration(servicePath string) *ServiceConfig {
	content, err := os.ReadFile(servicePath)
	if err != nil {
		return nil
	}

	config := &ServiceConfig{}

	// Extract token
	if re := regexp.MustCompile(`--token\s+(\S+)`); re.MatchString(string(content)) {
		matches := re.FindStringSubmatch(string(content))
		if len(matches) > 1 {
			config.Token = matches[1]
		}
	}

	// Extract watch path
	if re := regexp.MustCompile(`--watch\s+(\S+)`); re.MatchString(string(content)) {
		matches := re.FindStringSubmatch(string(content))
		if len(matches) > 1 {
			config.WatchPath = matches[1]
		}
	}

	// Extract API URL
	if re := regexp.MustCompile(`--api\s+(\S+)`); re.MatchString(string(content)) {
		matches := re.FindStringSubmatch(string(content))
		if len(matches) > 1 {
			config.APIURL = matches[1]
		}
	}

	// Extract processed directory
	if re := regexp.MustCompile(`--processed\s+(\S+)`); re.MatchString(string(content)) {
		matches := re.FindStringSubmatch(string(content))
		if len(matches) > 1 {
			config.ProcessedDir = matches[1]
		}
	}

	return config
}

// ServiceConfig holds the configuration for the systemd service
type ServiceConfig struct {
	Token        string
	WatchPath    string
	APIURL       string
	ProcessedDir string
}

// getServiceConfiguration prompts user for service configuration
// If existingConfig is provided, it will be used as default values
func getServiceConfiguration(existingConfig *ServiceConfig, homeDir string) (*ServiceConfig, error) {
	reader := bufio.NewReader(os.Stdin)
	config := &ServiceConfig{}

	// Set defaults based on user's home directory
	defaultToken := ""
	defaultWatchPath := filepath.Join(homeDir, "sathub", "data")
	defaultAPIURL := "https://api.sathub.de"
	defaultProcessedDir := filepath.Join(homeDir, "sathub", "processed")

	// Use existing values as defaults if available
	if existingConfig != nil {
		if existingConfig.Token != "" {
			defaultToken = existingConfig.Token
		}
		if existingConfig.WatchPath != "" {
			defaultWatchPath = existingConfig.WatchPath
		}
		if existingConfig.APIURL != "" {
			defaultAPIURL = existingConfig.APIURL
		}
		if existingConfig.ProcessedDir != "" {
			defaultProcessedDir = existingConfig.ProcessedDir
		}
	}

	// Prompt for token
	if defaultToken != "" {
		fmt.Printf("Enter station token [%s]: ", maskToken(defaultToken))
	} else {
		fmt.Print("Enter station token: ")
	}
	token, _ := reader.ReadString('\n')
	config.Token = strings.TrimSpace(token)
	if config.Token == "" {
		config.Token = defaultToken
	}
	if config.Token == "" {
		return nil, fmt.Errorf("token cannot be empty")
	}

	// Prompt for watch directory
	fmt.Printf("Enter watch directory [%s]: ", defaultWatchPath)
	watchPath, _ := reader.ReadString('\n')
	config.WatchPath = strings.TrimSpace(watchPath)
	if config.WatchPath == "" {
		config.WatchPath = defaultWatchPath
	}

	// Prompt for API URL
	fmt.Printf("Enter API URL [%s]: ", defaultAPIURL)
	apiURL, _ := reader.ReadString('\n')
	config.APIURL = strings.TrimSpace(apiURL)
	if config.APIURL == "" {
		config.APIURL = defaultAPIURL
	}

	// Prompt for processed directory
	fmt.Printf("Enter processed directory [%s]: ", defaultProcessedDir)
	processedDir, _ := reader.ReadString('\n')
	config.ProcessedDir = strings.TrimSpace(processedDir)
	if config.ProcessedDir == "" {
		config.ProcessedDir = defaultProcessedDir
	}

	return config, nil
}

// generateServiceFile generates the systemd user service file content
func generateServiceFile(config *ServiceConfig, binaryPath string) string {
	return fmt.Sprintf(`[Unit]
Description=SatHub Data Client
After=network.target

[Service]
Type=simple
ExecStart=%s --token %s --watch %s --api %s --processed %s
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`, binaryPath, config.Token, config.WatchPath, config.APIURL, config.ProcessedDir)
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
