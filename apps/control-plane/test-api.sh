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
SESSION_TOKEN=$(curl -f -s -X POST http://localhost:8080/api/v1/agents/register \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "test-agent-123",
    "hostname": "test-host",
    "version": "1.0.0",
    "capabilities": {
      "docker": true
    },
    "system": {
      "os": "linux",
      "arch": "amd64",
      "cpu_count": 8,
      "memory_bytes": 17179869184,
      "docker_version": "24.0.7"
    }
  }')
echo "$REGISTRATION" | jq .

AGENT_TOKEN=$(echo "$REGISTRATION" | jq -r '.session_token')
  }' | tee /dev/stderr | jq -r '.session_token')

if [ -z "$SESSION_TOKEN" ] || [ "$SESSION_TOKEN" = "null" ]; then
  echo "❌ Registration did not return a session token"
  kill $CONTROL_PLANE_PID
  rm -f control-plane-test
  exit 1
fi

echo ""
echo "🚫 Testing heartbeat without token (expect 401)..."
UNAUTH_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  http://localhost:8080/api/v1/agents/test-agent-123/heartbeat \
  -H "Content-Type: application/json" \
  -d '{"status": {"uptime": "30s", "running_services": 0}}')
if [ "$UNAUTH_STATUS" != "401" ]; then
  echo "❌ Expected 401, got $UNAUTH_STATUS"
  kill $CONTROL_PLANE_PID
  rm -f control-plane-test
  exit 1
fi

echo ""
echo "💓 Testing agent heartbeat with session token..."
curl -f -s -X POST http://localhost:8080/api/v1/agents/test-agent-123/heartbeat \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $SESSION_TOKEN" \
  -d '{
    "status": {
      "uptime": "30s",
      "running_services": 0
    },
    "system": {
      "os": "linux",
      "arch": "amd64",
      "cpu_count": 8,
      "memory_bytes": 17179869184,
      "docker_version": "24.0.7"
    }
  }' | jq .

echo ""
echo "📋 Testing list agents endpoint..."
curl -f -s http://localhost:8080/api/v1/agents | jq .

echo ""
echo "🔎 Testing get agent endpoint..."
curl -f -s http://localhost:8080/api/v1/agents/test-agent-123 \
  -H "Authorization: Bearer $SESSION_TOKEN" | jq .

echo ""
echo "🌐 Testing Stellar account link..."
curl -f -s -X POST http://localhost:8080/api/v1/stellar/accounts \
  -H "Content-Type: application/json" \
  -d '{
    "address": "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H",
    "label": "smoke-test"
  }' | jq .

echo ""
echo "🌐 Testing Stellar account list..."
curl -f -s http://localhost:8080/api/v1/stellar/accounts | jq .

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