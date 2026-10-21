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
curl -f -s -X POST http://localhost:8080/api/v1/agents/register \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "test-agent-123",
    "hostname": "test-host",
    "version": "1.0.0",
    "capabilities": {
      "docker": true
    }
  }' | jq .

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
echo "🧹 Cleaning up..."
kill $CONTROL_PLANE_PID
rm -f control-plane-test

echo "✅ All tests passed!"