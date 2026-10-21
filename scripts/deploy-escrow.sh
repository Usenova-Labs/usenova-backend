#!/usr/bin/env bash
#
# Manual smoke-test deployment of the PaymentEscrow contract to Stellar
# testnet. Requires the Stellar CLI: https://developers.stellar.org/docs/tools/developer-tools/cli/install-cli
#
# Usage:
#   ./scripts/deploy-escrow.sh [network]
#
# Defaults to the `testnet` network. This is a convenience wrapper around the
# steps documented in contracts/payment-escrow/README.md.

set -euo pipefail

NETWORK="${1:-testnet}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WASM="$ROOT_DIR/contracts/target/wasm32-unknown-unknown/release/payment_escrow.wasm"

if ! command -v stellar >/dev/null 2>&1; then
    echo "error: stellar CLI not found in PATH" >&2
    exit 1
fi

echo "Building payment-escrow contract..."
cd "$ROOT_DIR/contracts"
cargo build --release --target wasm32-unknown-unknown

if [ ! -f "$WASM" ]; then
    echo "error: wasm not found at $WASM" >&2
    exit 1
fi

echo "Ensuring 'consumer' identity exists..."
if ! stellar keys address consumer >/dev/null 2>&1; then
    stellar keys generate --network "$NETWORK" consumer
fi
CONSUMER_ADDR="$(stellar keys address consumer)"

echo "Ensuring 'provider' identity exists..."
if ! stellar keys address provider >/dev/null 2>&1; then
    stellar keys generate --network "$NETWORK" provider
fi
PROVIDER_ADDR="$(stellar keys address provider)"

echo "Funding identities via friendbot..."
curl -sf "https://friendbot.stellar.org/?addr=$CONSUMER_ADDR" >/dev/null || true
curl -sf "https://friendbot.stellar.org/?addr=$PROVIDER_ADDR" >/dev/null || true

echo "Deploying escrow contract..."
ESCROW_ID="$(stellar contract deploy --wasm "$WASM" --source consumer --network "$NETWORK")"
echo "ESCROW_ID=$ESCROW_ID"

echo "Deploying a test token contract..."
TOKEN_ID="$(stellar contract asset deploy --asset "USENOVA:$CONSUMER_ADDR" --source consumer --network "$NETWORK")"
echo "TOKEN_ID=$TOKEN_ID"

echo "Creating escrow (id=1, amount=400)..."
stellar contract invoke --id "$ESCROW_ID" --source consumer --network "$NETWORK" \
    -- create_escrow \
    --escrow_id 1 \
    --consumer "$CONSUMER_ADDR" \
    --provider "$PROVIDER_ADDR" \
    --token "$TOKEN_ID" \
    --amount 400

echo "Status after create:"
stellar contract invoke --id "$ESCROW_ID" --network "$NETWORK" \
    -- get_status --escrow_id 1

echo "Releasing escrow 1 to provider..."
stellar contract invoke --id "$ESCROW_ID" --source consumer --network "$NETWORK" \
    -- release --escrow_id 1

echo "Status after release:"
stellar contract invoke --id "$ESCROW_ID" --network "$NETWORK" \
    -- get_status --escrow_id 1

echo ""
echo "Done. Escrow contract: $ESCROW_ID"
echo "Add it to your .env as PAYMENT_ESCROW_CONTRACT_ID=$ESCROW_ID"
