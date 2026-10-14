#!/usr/bin/env bash

set -euo pipefail

# Test script for BYOI Platform components

echo "Running BYOI Platform tests..."

# Run Go tests
echo "Running Go tests..."
go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...
echo "✓ Go tests passed"

# Run Rust tests
echo "Running Rust tests..."
cd contracts
cargo test --verbose
cd ..
echo "✓ Rust tests passed"

echo ""
echo "✓ All tests passed!"
