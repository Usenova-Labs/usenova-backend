#!/usr/bin/env bash

set -euo pipefail

# Lint script for BYOI Platform components

echo "Running linters..."

# Check Go formatting
echo "Checking Go formatting..."
if [ -n "$(gofmt -l .)" ]; then
    echo "❌ Go code is not formatted. Run: gofmt -w ."
    gofmt -d .
    exit 1
fi
echo "✓ Go formatting OK"

# Check Go imports
echo "Checking Go imports..."
if command -v goimports &> /dev/null; then
    if [ -n "$(goimports -l .)" ]; then
        echo "❌ Go imports are not formatted. Run: goimports -w ."
        goimports -d .
        exit 1
    fi
    echo "✓ Go imports OK"
else
    echo "⚠ goimports not installed, skipping"
fi

# Check Rust formatting
echo "Checking Rust formatting..."
cd contracts
if ! cargo fmt -- --check; then
    echo "❌ Rust code is not formatted. Run: cargo fmt"
    exit 1
fi
echo "✓ Rust formatting OK"

# Run Clippy
echo "Running Clippy..."
if ! cargo clippy -- -D warnings; then
    echo "❌ Clippy found issues"
    exit 1
fi
echo "✓ Clippy OK"
cd ..

echo ""
echo "✓ All linters passed!"
