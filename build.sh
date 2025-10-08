#!/bin/bash

# Build script for SatHub client binary
# Creates binaries for multiple platforms

set -e

echo "Building SatHub client binaries for multiple platforms..."

# Create output directory
mkdir -p bin

# Define platforms to build for
PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
)

# Build for each platform
for platform in "${PLATFORMS[@]}"; do
    IFS='/' read -r GOOS GOARCH <<< "$platform"

    binary_name="sathub-client-${GOOS}-${GOARCH}"

    echo "Building for ${GOOS}/${GOARCH}..."
    CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build -a -installsuffix cgo -o "bin/${binary_name}" .

    # Verify the binary was created and is executable
    if [[ -f "bin/${binary_name}" ]]; then
        chmod +x "bin/${binary_name}"
        echo "✅ Built bin/${binary_name}"
    else
        echo "❌ Failed to build bin/${binary_name}"
        exit 1
    fi
done

echo
echo "Build complete! Binaries available in bin/:"
ls -la bin/