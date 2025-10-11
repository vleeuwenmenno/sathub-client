#!/bin/bash

# SatHub Client Installer
# Downloads and installs the latest sathub-client release from GitHub

set -e

# Configuration
REPO="vleeuwenmenno/sathub-client"
GITHUB_API="https://api.github.com/repos/${REPO}/releases/latest"
INSTALL_PATH="/usr/bin/sathub-client"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "This script must be run as root (sudo)"
        exit 1
    fi
}

# Detect OS and architecture
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case $OS in
        linux)
            OS="linux"
            ;;
        darwin)
            OS="darwin"
            ;;
        *)
            log_error "Unsupported OS: $OS"
            exit 1
            ;;
    esac

    case $ARCH in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        *)
            log_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    log_info "Detected platform: ${OS}-${ARCH}"
}

# Get latest release information from GitHub
get_latest_release() {
    log_info "Fetching latest release information..."

    if ! command -v curl &> /dev/null; then
        log_error "curl is required but not installed"
        exit 1
    fi

    # Fetch release data
    RELEASE_DATA=$(curl -s "$GITHUB_API")

    if [[ $? -ne 0 ]]; then
        log_error "Failed to fetch release data from GitHub"
        exit 1
    fi

    # Extract version tag
    LATEST_VERSION=$(echo "$RELEASE_DATA" | grep -o '"tag_name": "[^"]*' | cut -d'"' -f4)

    if [[ -z "$LATEST_VERSION" ]]; then
        log_error "Failed to extract version from GitHub response"
        exit 1
    fi

    log_info "Latest version: $LATEST_VERSION"
}

# Find the correct asset for current platform
find_asset() {
    ASSET_NAME="sathub-client-${OS}-${ARCH}"

    # Try to use jq if available for more reliable JSON parsing
    if command -v jq &> /dev/null; then
        DOWNLOAD_URL=$(echo "$RELEASE_DATA" | jq -r ".assets[] | select(.name == \"$ASSET_NAME\") | .browser_download_url")
    else
        # Fallback to grep/sed (less reliable)
        DOWNLOAD_URL=$(echo "$RELEASE_DATA" | grep -A 50 '"name": "'${ASSET_NAME}'"' | grep '"browser_download_url":' | head -1 | cut -d'"' -f4)
    fi

    if [[ -z "$DOWNLOAD_URL" ]]; then
        # Asset not found - check if this is a brand new release with missing assets
        check_new_release_status
        
        log_error "Could not find asset '${ASSET_NAME}' in release ${LATEST_VERSION}"
        log_error "Available assets:"
        if command -v jq &> /dev/null; then
            echo "$RELEASE_DATA" | jq -r '.assets[].name'
        else
            echo "$RELEASE_DATA" | grep '"name":' | cut -d'"' -f4
        fi
        exit 1
    fi

    log_info "Found asset: $ASSET_NAME"
}

# Check if release is brand new and possibly still building
check_new_release_status() {
    # Extract release creation time
    if command -v jq &> /dev/null; then
        PUBLISHED_AT=$(echo "$RELEASE_DATA" | jq -r '.published_at')
    else
        PUBLISHED_AT=$(echo "$RELEASE_DATA" | grep -o '"published_at": "[^"]*' | cut -d'"' -f4)
    fi

    if [[ -z "$PUBLISHED_AT" ]]; then
        return
    fi

    # Convert to Unix timestamp (requires date command with -d flag, available on Linux)
    if command -v date &> /dev/null; then
        PUBLISHED_TIMESTAMP=$(date -d "$PUBLISHED_AT" +%s 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%SZ" "$PUBLISHED_AT" +%s 2>/dev/null)
        CURRENT_TIMESTAMP=$(date +%s)
        
        if [[ -n "$PUBLISHED_TIMESTAMP" && -n "$CURRENT_TIMESTAMP" ]]; then
            AGE_MINUTES=$(( (CURRENT_TIMESTAMP - PUBLISHED_TIMESTAMP) / 60 ))
            
            if [[ $AGE_MINUTES -lt 15 ]]; then
                echo
                log_warning "This release was published ${AGE_MINUTES} minutes ago and assets are still being built"
                log_warning "The GitHub Actions build pipeline is likely still running"
                echo
                echo "Please wait a few minutes and try again, or check:"
                echo "  https://github.com/${REPO}/releases/tag/${LATEST_VERSION}"
                echo "  https://github.com/${REPO}/actions"
                echo
                exit 1
            fi
        fi
    fi
}

# Global flag to track if installation is needed
NEEDS_INSTALL=false

# Check if update is needed
check_update_needed() {
    if [[ -f "$INSTALL_PATH" ]]; then
        # Get currently installed version
        if INSTALLED_VERSION=$("$INSTALL_PATH" version 2>/dev/null); then
            # Extract version number (remove "SatHub Data Client " prefix if present)
            INSTALLED_VERSION=$(echo "$INSTALLED_VERSION" | sed 's/SatHub Data Client //')
            
            # Normalize versions by removing 'v' prefix for comparison
            COMPARE_LATEST="${LATEST_VERSION#v}"
            COMPARE_INSTALLED="${INSTALLED_VERSION#v}"
            
            # Compare versions (simple string comparison should work for semver)
            if [[ "$COMPARE_LATEST" == "$COMPARE_INSTALLED" ]]; then
                log_success "SatHub client is already up to date (${INSTALLED_VERSION})"
                NEEDS_INSTALL=false
            else
                log_info "Update available: ${INSTALLED_VERSION} → ${LATEST_VERSION}"
                NEEDS_INSTALL=true
            fi
        else
            log_warning "Could not determine installed version, proceeding with installation"
            NEEDS_INSTALL=true
        fi
    else
        log_info "SatHub client not installed, proceeding with fresh installation"
        NEEDS_INSTALL=true
    fi
}

# Download and install the binary
download_and_install() {
    if [[ "$NEEDS_INSTALL" == "false" ]]; then
        log_info "Skipping download - client is already up to date"
        return
    fi

    TMP_DIR=$(mktemp -d)
    TMP_FILE="${TMP_DIR}/sathub-client"

    log_info "Downloading ${ASSET_NAME}..."

    if ! curl -L -o "$TMP_FILE" "$DOWNLOAD_URL"; then
        log_error "Failed to download ${ASSET_NAME}"
        rm -rf "$TMP_DIR"
        exit 1
    fi

    # Make executable
    chmod +x "$TMP_FILE"

    # Verify the binary works
    if ! "$TMP_FILE" version &>/dev/null; then
        log_error "Downloaded binary appears to be invalid"
        rm -rf "$TMP_DIR"
        exit 1
    fi

    # Install to /usr/bin
    log_info "Installing to ${INSTALL_PATH}..."
    mv "$TMP_FILE" "$INSTALL_PATH"

    # Cleanup
    rm -rf "$TMP_DIR"

    log_success "SatHub client ${LATEST_VERSION} installed successfully!"
}

# Get the real user (when running with sudo)
get_real_user() {
    if [[ -n "$SUDO_USER" ]]; then
        echo "$SUDO_USER"
    else
        echo "$USER"
    fi
}

# Get the real user's ID (when running with sudo)
get_real_uid() {
    local real_user=$(get_real_user)
    id -u "$real_user"
}

# Run command as the real user with proper environment
run_as_user() {
    local real_user=$(get_real_user)
    local real_uid=$(get_real_uid)
    if [[ -n "$SUDO_USER" ]]; then
        sudo -u "$real_user" XDG_RUNTIME_DIR=/run/user/$real_uid "$@"
    else
        "$@"
    fi
}

# Setup systemd service
setup_service() {
    # Check if running on Linux with systemd
    if [[ "$(uname -s)" != "Linux" ]] || ! command -v systemctl &> /dev/null; then
        log_info "Systemd not available on this system, skipping service setup"
        return
    fi

    local real_user=$(get_real_user)
    local real_uid=$(get_real_uid)

    # Check if stdin is connected to a terminal (not piped from curl)
    if ! [ -t 0 ]; then
        # Running via pipe (e.g., curl | bash) - skip interactive prompts
        # Check if service exists for the real user
        if XDG_RUNTIME_DIR=/run/user/$real_uid run_as_user systemctl --user list-unit-files 2>/dev/null | grep -q "sathub-client.service"; then
            log_info "Systemd user service detected for user '$real_user' - restarting with updated binary..."
            if XDG_RUNTIME_DIR=/run/user/$real_uid run_as_user systemctl --user restart sathub-client 2>/dev/null; then
                log_success "Service restarted successfully"
            else
                log_warning "Failed to restart service, run: systemctl --user restart sathub-client"
            fi
            echo
            echo "To reconfigure service settings, run:"
            echo "  sathub-client install-service"
        else
            echo
            log_info "To set up automatic startup with systemd, run:"
            echo "  sathub-client install-service"
        fi
        return
    fi

    # Interactive mode - prompt user
    echo
    echo -e "${YELLOW}Service Setup${NC}"
    
    # Check if service already exists for the real user
    if XDG_RUNTIME_DIR=/run/user/$real_uid run_as_user systemctl --user list-unit-files 2>/dev/null | grep -q "sathub-client.service"; then
        echo "Systemd user service is already installed for user '$real_user'."
        echo -n "Would you like to reconfigure it? (y/N): "
    else
        echo -n "Would you like to set up a systemd user service for user '$real_user'? (y/N): "
    fi
    
    read -r response

    case $response in
        [Yy]|[Yy][Ee][Ss])
            log_info "Setting up systemd user service for user '$real_user'..."

            # Run install-service as the real user
            if ! run_as_user "$INSTALL_PATH" install-service; then
                log_error "Failed to setup systemd user service"
                exit 1
            fi

            log_success "Systemd user service setup complete!"
            echo
            echo "Service commands (run as '$real_user'):"
            echo "  Start:  systemctl --user start sathub-client"
            echo "  Stop:   systemctl --user stop sathub-client"
            echo "  Status: systemctl --user status sathub-client"
            echo "  Logs:   journalctl --user -u sathub-client -f"
            echo
            echo "To enable automatic start even when not logged in:"
            echo "  loginctl enable-linger $real_user"
            ;;
        *)
            log_info "Skipping service setup"
            echo
            echo "You can manually setup the service later with:"
            echo "  sathub-client install-service"
            ;;
    esac
}

# Main installation process
main() {
    echo -e "${BLUE}SatHub Client Installer${NC}"
    echo "=========================="
    echo

    check_root
    detect_platform
    get_latest_release
    find_asset
    check_update_needed
    download_and_install
    setup_service

    echo
    if [[ "$NEEDS_INSTALL" == "true" ]]; then
        log_success "Installation complete!"
        echo
        echo "Usage:"
        echo "  sathub-client --help"
        echo "  sathub-client --token YOUR_TOKEN --watch /path/to/data"
    else
        log_success "Client is up to date!"
    fi
}

# Run main function
main "$@"
