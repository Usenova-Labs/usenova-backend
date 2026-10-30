#!/usr/bin/env bash
#
# Manual check for the control plane's read-only deployment escrow status path.
#
# Usage:
#   CONTROL_PLANE_ADMIN_TOKEN=... ./scripts/check-escrow-status.sh <deployment-id> [escrow-id] [expected-status]
#
# When escrow-id is supplied the script first links it to the deployment
# (PUT /api/v1/deployments/:id/escrow), then reads the on-chain status through
# the control plane (GET /api/v1/deployments/:id/escrow). This requires the
# control plane to be configured with PAYMENT_ESCROW_CONTRACT_ID and access to
# the configured Stellar/Soroban RPC endpoint.

set -euo pipefail

BASE_URL="${CONTROL_PLANE_URL:-${NOVA_CONTROL_PLANE_URL:-http://127.0.0.1:8080}}"
BASE_URL="${BASE_URL%/}"

if [ -z "${CONTROL_PLANE_ADMIN_TOKEN:-}" ]; then
    echo "error: CONTROL_PLANE_ADMIN_TOKEN is required (operator token)" >&2
    exit 1
fi

DEPLOYMENT_ID="${1:-${DEPLOYMENT_ID:-}}"
ESCROW_ID="${2:-${ESCROW_ID:-}}"
EXPECTED_STATUS="${3:-${EXPECTED_STATUS:-}}"

if [ -z "$DEPLOYMENT_ID" ]; then
    echo "usage: $0 <deployment-id> [escrow-id] [expected-status]" >&2
    exit 1
fi

auth_header="Authorization: Bearer ${CONTROL_PLANE_ADMIN_TOKEN}"

if [ -n "$ESCROW_ID" ]; then
    echo "Linking escrow ${ESCROW_ID} to deployment ${DEPLOYMENT_ID}..."
    curl -fsS -X PUT "${BASE_URL}/api/v1/deployments/${DEPLOYMENT_ID}/escrow" \
        -H "$auth_header" \
        -H 'Content-Type: application/json' \
        -d "{\"escrow_id\":${ESCROW_ID}}"
    echo
fi

echo "Reading escrow status for deployment ${DEPLOYMENT_ID}..."
BODY="$(curl -fsS "${BASE_URL}/api/v1/deployments/${DEPLOYMENT_ID}/escrow" -H "$auth_header")"
printf '%s\n' "$BODY"

if [ -n "$EXPECTED_STATUS" ]; then
    ACTUAL_STATUS="$(printf '%s' "$BODY" | grep -o '"status":"[^"]*"' | cut -d'"' -f4 || true)"
    if [ "$ACTUAL_STATUS" != "$EXPECTED_STATUS" ]; then
        echo "error: expected status ${EXPECTED_STATUS}, got ${ACTUAL_STATUS:-<missing>}" >&2
        exit 1
    fi
    echo "Status matches expected value: ${EXPECTED_STATUS}"
fi
