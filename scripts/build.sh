#!/usr/bin/env bash

set -euo pipefail

# Build script for Usenova components

VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"
BUILD_DIR="bin"

echo "Building Usenova..."
echo "Version: $VERSION"
echo "Commit: $COMMIT"

# Create build directory
mkdir -p "$BUILD_DIR"

# Build CLI
echo "Building CLI..."
cd apps/cli
go build -ldflags="-X main.version=$VERSION -X main.commit=$COMMIT" -o "../../$BUILD_DIR/nova"
cd ../..
echo "✓ CLI built: $BUILD_DIR/nova"

# Build Agent
echo "Building Agent..."
cd apps/agent
go build -ldflags="-X main.version=$VERSION -X main.commit=$COMMIT" -o "../../$BUILD_DIR/usenova-agent"
cd ../..
echo "✓ Agent built: $BUILD_DIR/usenova-agent"

# Build Control Plane
echo "Building Control Plane..."
cd apps/control-plane
go build -ldflags="-X main.version=$VERSION -X main.commit=$COMMIT" -o "../../$BUILD_DIR/nova-control"
cd ../..
echo "✓ Control Plane built: $BUILD_DIR/nova-control"

# Build Smart Contracts
echo "Building Smart Contracts..."
cd contracts
cargo build --release --target wasm32-unknown-unknown
cd ..
echo "✓ Smart Contracts built: contracts/target/wasm32-unknown-unknown/release/"

echo ""
echo "✓ Build complete!"
echo "Binaries available in: $BUILD_DIR/"
