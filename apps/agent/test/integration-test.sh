#!/usr/bin/env bash

set -euo pipefail

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${BLUE}🚀 Running Integration Test${NC}"
echo "================================"

# Change to agent directory
cd "$(dirname "$0")/.."

# Build agent
echo -e "${BLUE}Building agent...${NC}"
go build -o ./test/agent-test

# Start mock server in background
echo -e "${BLUE}Starting mock control plane...${NC}"
go run test/mock-control-plane.go &
MOCK_PID=$!

# Wait for server to start
sleep 2

# Function to cleanup
cleanup() {
    echo -e "${YELLOW}Cleaning up...${NC}"
    kill $MOCK_PID 2>/dev/null || true
    kill $AGENT_PID 2>/dev/null || true
    rm -f ./test/agent-test
    wait
}
trap cleanup EXIT

# Test the agent
echo -e "${BLUE}Starting agent...${NC}"
export CONTROL_PLANE_URL=http://localhost:8080
export AGENT_TOKEN=test-token-123
export HEARTBEAT_INTERVAL=5s
export DEBUG=1

./test/agent-test &
AGENT_PID=$!

echo -e "${GREEN}✅ Integration test running!${NC}"
echo "Watch the logs above for:"
echo "  - Agent registration"
echo "  - Heartbeat messages every 5 seconds"
echo "Press Ctrl+C to stop..."

# Wait for user interrupt
wait $AGENT_PID