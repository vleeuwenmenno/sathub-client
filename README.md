# SatHub Data Client

A standalone binary that monitors directories for satellite data from sathub and automatically pushes complete satellite passes to the SatHub API.

## Features

- **One-Command Installation**: Built-in install and service setup commands
- **Auto-Service Management**: Automatic systemd service creation and configuration
- **Directory Monitoring**: Watches for complete satellite pass directories from sathub
- **Processing Delay**: Configurable delay before processing to allow sathub to complete
- **Complete Pass Processing**: Handles all data from a satellite pass (metadata, CBOR, images)
- **Cross-Platform**: Binaries for Linux (x86_64 and ARM64), Windows (x86_64), and macOS (Intel and Apple Silicon)
- **Station Health**: Sends periodic health checks to keep station online
- **Error Recovery**: Automatic retry with configurable backoff
- **Flexible Configuration**: Command-line arguments with environment variable fallbacks
- **Rich Logging**: Structured logging with zerolog for better debugging

## Quick Installation & Setup (Recommended)

The easiest way to get started is with the built-in installation commands that handle everything automatically:

### Quick Setup

1. Create an account on [sathub.de](https://sathub.de)
2. Create a ground station and note your **Station API Token**
3. Install the client:

**Linux/macOS (Easiest - One-line install):**

```bash
curl -sSL https://api.sathub.de/install | sudo bash
```

**Manual Download:**

Download the client for your platform from [Releases](https://github.com/vleeuwenmenno/sathub-client/releases) and run:

```bash
# Linux/macOS - make executable first
chmod +x sathub-client-*
./sathub-client-* --token YOUR_STATION_TOKEN --watch /path/to/your/images

# Windows
sathub-client-windows-amd64.exe --token YOUR_STATION_TOKEN --watch C:\path\to\your\images
```

The client will monitor the specified directory and automatically upload new satellite captures to your SatHub station.

### 2. Setup as User Service

Configure and start the systemd user service with guided setup:

```bash
# Setup systemd user service (interactive configuration - no sudo needed!)
sathub-client install-service

# The installer will:
# - Create required directories in your home directory
# - Prompt for your station token
# - Configure watch and processed directories
# - Enable and start the service automatically
```

**That's it!** Your SatHub client is now installed and running as a user service.

### Available Commands

The SatHub client now includes built-in installation and management commands:

| Command                         | Description                                                    |
| ------------------------------- | -------------------------------------------------------------- |
| `sathub-client`                 | Run the client normally (requires token and watch directory)   |
| `sathub-client install`         | Install the binary to `/usr/bin/sathub-client` (requires sudo) |
| `sathub-client install-service` | Setup systemd user service with guided configuration           |
| `sathub-client version`         | Show version information                                       |

### Update Token

If you need to update your station token later:

```bash
# Choose "y" when asked if you want to update the configuration
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

The client supports both command-line arguments and environment variables. Command-line arguments take precedence over environment variables.

### Command-Line Arguments

```bash
sathub-client --help
```

| Flag          | Short | Default                 | Description                            |
| ------------- | ----- | ----------------------- | -------------------------------------- |
| `--token`     | `-t`  | _required_              | Station API token                      |
| `--watch`     | `-w`  | _required_              | Directory path to watch for new images |
| `--api`       | `-a`  | `https://api.sathub.de` | SatHub API URL                         |
| `--processed` | `-p`  | `./processed`           | Directory to move processed files      |
| `--verbose`   | `-v`  | `false`                 | Enable verbose (debug) logging         |

### Environment Variables (Fallbacks)

| Variable        | Description                                                               |
| --------------- | ------------------------------------------------------------------------- |
| `STATION_TOKEN` | Station authentication token (fallback for `--token`)                     |
| `WATCH_PATHS`   | Directory to monitor (fallback for `--watch`)                             |
| `API_URL`       | SatHub API base URL (fallback for `--api`)                                |
| `PROCESSED_DIR` | Directory to move processed satellite passes (fallback for `--processed`) |

## Data Format

The client processes complete satellite pass directories from sathub output. Each directory should contain:

### Required Files:

- `dataset.json` - Main metadata file with satellite information
- At least one product directory (e.g., `MSU-MR/`, `MSU-MR (Filled)/`) containing:
  - `product.cbor` - Binary satellite data
  - Multiple `.png` images from the processed data

### Example Directory Structure:

```
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

### dataset.json Format:

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

### Command-Line Usage

```bash
# Linux/macOS
./sathub-client --token YOUR_STATION_TOKEN --watch /path/to/satellite/data

# Windows
sathub-client.exe --token YOUR_STATION_TOKEN --watch C:\path\to\satellite\data

# With custom API server
./sathub-client --token YOUR_TOKEN --watch /path/to/data --api https://my-sathub.example.com

# Enable verbose logging
./sathub-client --token YOUR_TOKEN --watch /path/to/data --verbose

# Custom processed directory
./sathub-client --token YOUR_TOKEN --watch /path/to/data --processed /path/to/archive
```

### Environment Variable Usage (Legacy)

You can still use environment variables as an alternative to command-line arguments:

```bash
# Set environment variables
export STATION_TOKEN=your_station_token_from_sathub
export WATCH_PATHS=/path/to/satellite/data
export API_URL=https://api.sathub.de

# Run the client (no arguments needed)
./sathub-client
```

### Mixed Usage

Environment variables provide defaults that can be overridden by command-line arguments:

```bash
# Set defaults via environment
export STATION_TOKEN=your_default_token
export API_URL=https://api.sathub.de

# Override watch path via command line
./sathub-client --watch /different/path --verbose
```

### Systemd User Service (Linux)

**Recommended**: Use the built-in service installer:

```bash
# Install binary and setup user service automatically (no sudo required!)
sathub-client install-service
```

This will:

- Create the systemd user service file automatically
- Create required directories in your home directory
- Prompt for your station token and configuration
- Enable and start the service

**Managing the service:**

```bash
# Start the service
systemctl --user start sathub-client

# Stop the service
systemctl --user stop sathub-client

# Check service status
systemctl --user status sathub-client

# View logs
journalctl --user -u sathub-client -f

# Enable automatic start even when not logged in
loginctl enable-linger $USER
```

**Manual service configuration** (if you prefer manual setup):

Create `~/.config/systemd/user/sathub-client.service`:

```ini
[Unit]
Description=SatHub Data Client
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/sathub-client --token your_token_here --watch /home/yourusername/sathub/data --api https://api.sathub.de --processed /home/yourusername/sathub/processed
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
```

Then enable and start manually:

```bash
systemctl --user daemon-reload
systemctl --user enable sathub-client
systemctl --user start sathub-client
systemctl --user status sathub-client
```

## Error Handling

- **Failed API calls** are retried with configurable backoff
- **Incomplete directories** are skipped until they contain required files
- **Processing failures** mark directories for retry (not moved to processed)
- **Health check failures** are logged but don't stop processing
- **Invalid satellite passes** are logged and skipped
