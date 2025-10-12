# SatHub Data Client

A standalone binary that monitors directories for satellite data from SatDump and automatically pushes complete satellite passes to the SatHub API.

## Features

- **One-Command Installation**: Built-in install and service setup commands
- **Auto-Service Management**: Automatic systemd service creation and configuration
- **YAML Configuration**: Simple, structured configuration file
- **Directory Monitoring**: Watches for complete satellite pass directories from SatDump
- **Processing Delay**: Configurable delay before processing to allow SatDump to complete
- **Complete Pass Processing**: Handles all data from a satellite pass (metadata, CBOR, images)
- **Cross-Platform**: Binaries for Linux (x86_64 and ARM64), Windows (x86_64), and macOS (Intel and Apple Silicon)
- **Station Health**: Sends periodic health checks to keep station online
- **Error Recovery**: Automatic retry with configurable backoff
- **Rich Logging**: Structured logging with zerolog for better debugging

## Quick Installation & Setup (Recommended)

The easiest way to get started is with the built-in installation commands that handle everything automatically:

### Quick Setup

1. Create an account on [sathub.de](https://sathub.de)
2. Create a ground station and note your **Station API Token**
3. Install and configure the client:

**Linux/macOS (One-line install):**

```bash
curl -sSL https://api.sathub.de/install | bash
```

**After installation, setup the service:**

```bash
# Setup systemd user service (interactive configuration - no sudo needed!)
sathub-client install-service

# The installer will:
# - Install the binary to ~/.local/bin/sathub-client
# - Create configuration file at ~/.config/sathub-client/config.yaml
# - Create required directories (~/sathub/data, ~/sathub/processed)
# - Prompt for your station token
# - Enable and start the service automatically
```

**That's it!** Your SatHub client is now installed and running as a user service.

### Available Commands

The SatHub client includes built-in installation and management commands:

| Command                           | Description                                          |
| --------------------------------- | ---------------------------------------------------- |
| `sathub-client`                   | Run the client (requires config file)                |
| `sathub-client --config <path>`   | Run with custom config file location                 |
| `sathub-client install`           | Install the binary to `~/.local/bin/sathub-client`   |
| `sathub-client install-service`   | Setup systemd user service with guided configuration |
| `sathub-client uninstall-service` | Stop and remove systemd user service                 |
| `sathub-client update`            | Update to the latest version                         |
| `sathub-client version`           | Show version information                             |

### Update Configuration or Token

To update your configuration (including station token):

```bash
# Edit the config file directly
nano ~/.config/sathub-client/config.yaml

# Or re-run the service installer to update settings
sathub-client install-service
```

## Manual Installation

### Download Pre-built Binary

Download the appropriate binary for your platform from the [GitHub releases page](https://github.com/vleeuwenmenno/sathub/releases):

- **Linux (x86_64)**: `sathub-client-linux-amd64`
- **Linux (ARM64)**: `sathub-client-linux-arm64` (Raspberry Pi compatible)
- **Windows (x86_64)**: `sathub-client-windows-amd64.exe`
- **macOS (Intel)**: `sathub-client-darwin-amd64`
- **macOS (Apple Silicon)**: `sathub-client-darwin-arm64`

### Build from Source

```bash
cd client
go build -o bin/sathub-client
```

This creates the `sathub-client` binary in the `bin` directory.

## Configuration

The client uses a YAML configuration file located at `~/.config/sathub-client/config.yaml` by default.

### Configuration File Format

```yaml
station:
  token: "your_station_token_here"
  api_url: "https://api.sathub.de"

paths:
  watch: "/home/yourusername/sathub/data"
  processed: "/home/yourusername/sathub/processed"

intervals:
  health_check: 300 # seconds (5 minutes)
  process_delay: 60 # seconds (wait before processing new directories)

options:
  insecure: false # Set to true for self-signed certificates (development)
  verbose: false # Enable debug logging
```

### Configuration Options

| Section     | Option          | Default                 | Description                                       |
| ----------- | --------------- | ----------------------- | ------------------------------------------------- |
| `station`   | `token`         | _required_              | Station API token from SatHub                     |
| `station`   | `api_url`       | `https://api.sathub.de` | SatHub API URL                                    |
| `paths`     | `watch`         | `~/sathub/data`         | Directory to monitor for new satellite passes     |
| `paths`     | `processed`     | `~/sathub/processed`    | Directory to move processed files                 |
| `intervals` | `health_check`  | `300`                   | Health check interval in seconds (5 minutes)      |
| `intervals` | `process_delay` | `60`                    | Delay before processing new directories (seconds) |
| `options`   | `insecure`      | `false`                 | Allow insecure HTTPS connections                  |
| `options`   | `verbose`       | `false`                 | Enable verbose (debug) logging                    |

### Custom Configuration File

You can specify a custom configuration file location:

```bash
sathub-client --config /path/to/custom-config.yaml
```

## Data Format

The client processes complete satellite pass directories from SatDump output. Each directory should contain:

### Required Files

- `dataset.json` - Main metadata file with satellite information
- At least one product directory (e.g., `MSU-MR/`, `MSU-MR (Filled)/`) containing:
  - `product.cbor` - Binary satellite data
  - Multiple `.png` images from the processed data

### Example Directory Structure

```text
2025-09-26_13-01_meteor_m2-x_lrpt_137.9 MHz/
├── dataset.json          # Satellite metadata
├── meteor_m2-x_lrpt.cadu # CADU data (optional)
├── MSU-MR/               # Product directory
│   ├── product.cbor      # CBOR data
│   ├── MSU-MR-1.png      # Processed images
│   ├── MSU-MR-2.png
│   └── ...
└── MSU-MR (Filled)/      # Additional product directory
    ├── product.cbor
    └── ...
```

### dataset.json Format

The client extracts comprehensive satellite and dataset information from `dataset.json`:

```json
{
  "timestamp": "2024-09-27T16:45:00Z",
  "satellite_name": "METEOR-M2",
  "satellite": "METEOR-M2", // Alternative field
  "name": "METEOR-M2", // Alternative field
  "norad": 40069,
  "frequency": 137.9,
  "modulation": "LRPT",
  "datasets": [
    { "name": "MSU-MR", "type": "thermal" },
    { "name": "MSU-MR (Filled)", "type": "thermal" }
  ],
  "products": [
    { "name": "MSU-MR-1", "description": "Thermal infrared" },
    { "name": "MSU-MR-2", "description": "Near infrared" }
  ],
  "additional_metadata": "custom data"
}
```

**Extracted Information:**

- Satellite name (with fallbacks: `satellite_name` → `satellite` → `name`)
- Timestamp and NORAD ID
- Frequency and modulation details
- Dataset and product listings
- All other fields preserved in metadata

All PNG images from product directories are automatically uploaded as post images.

## Usage

### Running the Client

After configuring, simply run:

```bash
# Run with default config location (~/.config/sathub-client/config.yaml)
sathub-client

# Run with custom config file
sathub-client --config /path/to/config.yaml
```

The client will:

1. Monitor the configured watch directory for new satellite pass directories
2. Wait for the configured process delay to ensure SatDump has finished
3. Automatically upload complete passes (metadata, CBOR data, images) to your SatHub station
4. Send periodic health checks to keep your station marked as online
5. Move processed directories to the configured processed directory

### Example Output

```text
2024-01-15T10:30:00Z INF Starting SatHub Data Client version=v2.0.0 api_url=https://api.sathub.de
2024-01-15T10:30:00Z INF Configuration parameters health_check_interval=300 process_delay=60
2024-01-15T10:30:00Z INF Starting directory watcher path=/home/user/sathub/data
2024-01-15T10:35:23Z INF New directory detected dir=2024-01-15_10-34_NOAA-19_apt_137.1MHz
2024-01-15T10:36:23Z INF Processing complete satellite pass
2024-01-15T10:36:25Z INF Post created successfully post_id=abc123
2024-01-15T10:36:27Z INF Uploaded 3 images
2024-01-15T10:36:28Z INF Successfully processed satellite pass
```

### Systemd User Service (Linux)

**Recommended**: Use the built-in service installer:

```bash
# Install binary and setup user service automatically (no sudo required!)
sathub-client install-service
```

This will:

- Install the binary to `~/.local/bin/sathub-client`
- Create the systemd user service file automatically
- Create configuration file at `~/.config/sathub-client/config.yaml`
- Create required directories in your home directory
- Prompt for your station token and configuration
- Enable and start the service

**Managing the service:**

```bash
# Check service status
systemctl --user status sathub-client

# View logs (follow mode)
journalctl --user -u sathub-client -f

# View recent logs
journalctl --user -u sathub-client -n 100

# Restart service (after config changes)
systemctl --user restart sathub-client

# Stop the service
systemctl --user stop sathub-client

# Start the service
systemctl --user start sathub-client

# Disable service
systemctl --user disable sathub-client

# Enable automatic start even when not logged in
loginctl enable-linger $USER
```

**Uninstall the service:**

```bash
sathub-client uninstall-service
```

## Error Handling

- **Failed API calls** are retried with configurable backoff
- **Incomplete directories** are skipped until they contain required files
- **Processing failures** mark directories for retry (not moved to processed)
- **Health check failures** are logged but don't stop processing
- **Invalid satellite passes** are logged and skipped
