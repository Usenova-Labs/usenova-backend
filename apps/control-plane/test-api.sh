#!/bin/bash

# Test script for Control Plane API
set -e

echo "🧪 Testing Control Plane API endpoints..."
echo "Starting control plane in background..."

# Operator token for the command API. The command API stays disabled when no
# token is configured.
export CONTROL_PLANE_ADMIN_TOKEN="test-operator-token"

# Build and run control plane in background
go build -o control-plane-test
./control-plane-test &
CONTROL_PLANE_PID=$!

# Wait for server to start
echo "Waiting for server to start..."
sleep 3

echo ""
echo "🔍 Testing health endpoint..."
curl -f -s http://localhost:8080/health | jq .

echo ""
echo "🤖 Testing agent registration..."
REGISTRATION=$(curl -f -s -X POST http://localhost:8080/api/v1/agents/register \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "test-agent-123",
    "hostname": "test-host",
    "version": "1.0.0",
    "capabilities": {
      "docker": true
    }
  }')
echo "$REGISTRATION" | jq .

AGENT_TOKEN=$(echo "$REGISTRATION" | jq -r '.session_token')

echo ""
echo "💓 Testing agent heartbeat..."
curl -f -s -X POST http://localhost:8080/api/v1/agents/test-agent-123/heartbeat \
  -H "Content-Type: application/json" \
  -d '{
    "status": {
      "uptime": "30s",
      "running_services": 0
    }
  }' | jq .

echo ""
echo "📋 Testing list agents endpoint..."
curl -f -s http://localhost:8080/api/v1/agents | jq .

echo ""
echo "🔎 Testing get agent endpoint..."
curl -f -s http://localhost:8080/api/v1/agents/test-agent-123 | jq .

echo ""
echo "🚫 Testing that the command API rejects a missing operator token..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  http://localhost:8080/api/v1/agents/test-agent-123/commands \
  -H "Content-Type: application/json" \
  -d '{"type": "stop", "payload": {"name": "demo"}}')
if [ "$STATUS" != "401" ]; then
  echo "Expected 401, got $STATUS"
  exit 1
fi
echo "Got $STATUS as expected"

echo ""
echo "📤 Testing command enqueue..."
COMMAND=$(curl -f -s -X POST http://localhost:8080/api/v1/agents/test-agent-123/commands \
  -H "Authorization: Bearer $CONTROL_PLANE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "deploy",
    "payload": {
      "name": "demo",
      "image": "nginx:latest"
    }
  }')
echo "$COMMAND" | jq .

COMMAND_ID=$(echo "$COMMAND" | jq -r '.command.id')

echo ""
echo "📥 Testing command claim as the agent..."
curl -f -s http://localhost:8080/api/v1/agents/test-agent-123/commands/next \
  -H "Authorization: Bearer $AGENT_TOKEN" | jq .

echo ""
echo "🚫 Testing that another agent cannot claim the command..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  http://localhost:8080/api/v1/agents/test-agent-123/commands/next \
  -H "Authorization: Bearer not-the-agent-token")
if [ "$STATUS" != "401" ]; then
  echo "Expected 401, got $STATUS"
  exit 1
fi
echo "Got $STATUS as expected"

echo ""
echo "📨 Testing command result reporting..."
curl -f -s -X POST \
  "http://localhost:8080/api/v1/agents/test-agent-123/commands/$COMMAND_ID/result" \
  -H "Authorization: Bearer $AGENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status": "succeeded", "message": "service demo deployed"}' | jq .

echo ""
echo "🧹 Cleaning up..."
kill $CONTROL_PLANE_PID
rm -f control-plane-test

echo "✅ All tests passed!"