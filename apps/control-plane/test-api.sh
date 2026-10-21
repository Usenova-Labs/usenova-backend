#!/bin/bash

# Test script for Control Plane API
set -e

echo "🧪 Testing Control Plane API endpoints..."
echo "Starting control plane in background..."

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
SESSION_TOKEN=$(curl -f -s -X POST http://localhost:8080/api/v1/agents/register \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "test-agent-123",
    "hostname": "test-host",
    "version": "1.0.0",
    "capabilities": {
      "docker": true
    }
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
echo "🧹 Cleaning up..."
kill $CONTROL_PLANE_PID
rm -f control-plane-test

echo "✅ All tests passed!"