#!/usr/bin/env bash

set -euo pipefail

echo "🧪 Usenova Agent Testing Suite"
echo "=============================="
echo

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_step() {
    echo -e "${BLUE}📝 $1${NC}"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Change to agent directory
cd "$(dirname "$0")/.."

print_step "Step 1: Running Unit Tests"
echo "Testing configuration management..."
go test -v ./internal/config

echo
echo "Testing Control Plane client..."
go test -v ./internal/controlplane

print_success "Unit tests completed"
echo

print_step "Step 2: Building Agent Binary"
go build -o ./test/usenova-agent-test
print_success "Agent built successfully"
echo

print_step "Step 3: Testing Configuration Validation"

echo "Testing missing CONTROL_PLANE_URL..."
if ./test/usenova-agent-test 2>/dev/null; then
    print_error "Agent should fail without CONTROL_PLANE_URL"
    exit 1
else
    print_success "Correctly failed without CONTROL_PLANE_URL"
fi

echo "Testing missing AGENT_TOKEN..."
if CONTROL_PLANE_URL=http://test ./test/usenova-agent-test 2>/dev/null; then
    print_error "Agent should fail without AGENT_TOKEN"
    exit 1
else
    print_success "Correctly failed without AGENT_TOKEN"
fi

echo "Testing invalid heartbeat interval..."
if CONTROL_PLANE_URL=http://test AGENT_TOKEN=test HEARTBEAT_INTERVAL=1s ./test/usenova-agent-test 2>/dev/null; then
    print_error "Agent should fail with heartbeat interval too short"
    exit 1
else
    print_success "Correctly failed with invalid heartbeat interval"
fi

print_success "Configuration validation tests completed"
echo

print_step "Step 4: Instructions for Manual Testing"
echo
echo "To test the complete agent flow:"
echo
echo "Terminal 1 - Start Mock Control Plane:"
echo "  cd apps/agent"
echo "  go run test/mock-control-plane.go"
echo
echo "Terminal 2 - Run Agent:"
echo "  cd apps/agent"
echo "  export CONTROL_PLANE_URL=http://localhost:8080"
echo "  export AGENT_TOKEN=test-token-123"
echo "  export DEBUG=1  # Optional: Enable debug logging"
echo "  go run main.go"
echo
echo "Expected behavior:"
echo "  1. Agent starts and logs configuration"
echo "  2. Agent registers with mock control plane (3 retry attempts if needed)"
echo "  3. Agent sends heartbeats every 30 seconds"
echo "  4. Mock server logs all registration and heartbeat requests"
echo "  5. Press Ctrl+C to test graceful shutdown"
echo

print_step "Step 5: Testing Different Scenarios"
echo
echo "Test with custom heartbeat interval:"
echo "  export HEARTBEAT_INTERVAL=10s"
echo
echo "Test with invalid Control Plane URL:"
echo "  export CONTROL_PLANE_URL=http://invalid-server:9999"
echo "  # Should retry 3 times then exit"
echo
echo "Test Docker capability detection:"
echo "  # Check if Docker is detected correctly in agent logs"
echo

print_success "All automated tests passed!"
print_warning "Run the manual tests above to verify full functionality"

# Cleanup
rm -f ./test/usenova-agent-test

echo
echo "🎯 Next steps:"
echo "  1. Run the mock server test scenario above"
echo "  2. Implement the Control Plane endpoints"
echo "  3. Add service deployment functionality"