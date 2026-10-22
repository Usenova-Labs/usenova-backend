# Stellar Testnet Setup

> How to link a Stellar account and query its balance on testnet.

**Status**: The control plane exposes account-link and balance endpoints and
the CLI has matching `nova stellar` commands. This document covers testnet
only; mainnet is out of scope for the MVP.

## Overview

The Stellar integration is deliberately minimal:

1. **Link** a public Stellar account address with the control plane. Only the
   public address is stored; signing keys are never handled by the control
   plane or CLI.
2. **Query balances** (native XLM or a test asset such as USDC) through the
   control plane, which reads them from Horizon.

No mainnet keys, account addresses, or secrets are committed to the repository.
All network configuration is environment-driven.

## Prerequisites

- A Stellar testnet account (an unfunded account cannot be queried; fund it
  with [Friendbot](https://developers.stellar.org/docs/tutorials/create-account)).
- The control plane and CLI built locally (`./scripts/build.sh`).
- Network access to `https://horizon-testnet.stellar.org`.

## Configuration

All values are optional and default to testnet. Set them in your environment
(or a gitignored `.env`):

| Variable | Purpose | Default |
| --- | --- | --- |
| `STELLAR_NETWORK` | `testnet` \| `mainnet` | `testnet` |
| `STELLAR_HORIZON_URL` | Horizon endpoint for balance queries | `https://horizon-testnet.stellar.org` |
| `STELLAR_RPC_URL` | Soroban RPC endpoint (escrow helpers) | `https://soroban-testnet.stellar.org` |
| `STELLAR_DEFAULT_ASSET` | Asset used when a balance query omits one | `XLM` |
| `PAYMENT_ESCROW_CONTRACT_ID` | Escrow contract id (optional) | (empty) |
| `USENOVA_CONTROL_PLANE_URL` | Control plane URL used by the CLI | `http://127.0.0.1:8080` |

`STELLAR_NETWORK` selects the network; the URLs are derived from it unless you
override them explicitly.

## Create and fund a testnet account

Using the [Stellar CLI](https://developers.stellar.org/docs/tools/developer-tools/cli/install-cli):

```bash
stellar keys generate --network testnet demo
ADDRESS=$(stellar keys address demo)
echo "$ADDRESS"                       # starts with G, 56 characters

# Fund it (testnet only)
curl "https://friendbot.stellar.org/?addr=$ADDRESS"
```

You can also paste any existing testnet public key (`G...`). Never use a
mainnet or valuable key here.

## Link the account

Start the control plane, then link the address with the CLI:

```bash
# terminal 1
./bin/nova-control

# terminal 2
export USENOVA_CONTROL_PLANE_URL=http://127.0.0.1:8080
./bin/nova stellar link "$ADDRESS" --label payouts
./bin/nova stellar accounts
```

Or call the API directly:

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/stellar/accounts \
  -H 'Content-Type: application/json' \
  -d "{\"address\":\"$ADDRESS\",\"label\":\"payouts\"}"
```

## Query balances

```bash
# All balances (XLM and any trustlines)
./bin/nova stellar balance "$ADDRESS"

# A single asset
./bin/nova stellar balance "$ADDRESS" --asset USDC

# JSON output for scripting
./bin/nova stellar balance "$ADDRESS" -o json
```

Direct API equivalent:

```bash
curl -s "http://127.0.0.1:8080/api/v1/stellar/accounts/$ADDRESS/balance"
curl -s "http://127.0.0.1:8080/api/v1/stellar/accounts/$ADDRESS/balance?asset=USDC"
```

Amounts are returned as strings exactly as Horizon reports them (7 decimal
places); the CLI does not parse them into floats, so no precision is lost.

## API reference

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/stellar/accounts` | Link an account (`{"address","label"}`) |
| `GET` | `/api/v1/stellar/accounts` | List linked accounts |
| `DELETE` | `/api/v1/stellar/accounts/:address` | Unlink an account |
| `GET` | `/api/v1/stellar/accounts/:address/balance` | Query balances (`?asset=` optional) |

Error responses use the standard control plane envelope:

```json
{ "error": "Invalid Stellar address", "message": "expected 56 characters" }
```

## USDC on testnet

To hold a test USDC balance, add a trustline to a testnet USDC issuer and
acquire some via the issuer's testnet faucet. Once the trustline exists,
Horizon reports the `USDC` balance and `nova stellar balance <address> --asset USDC`
returns it. The exact issuer varies; use whichever testnet issuer your
organization standardizes on and record it outside the repository.

## Security notes

- Only public addresses are stored and transmitted; secret keys never reach the
  control plane or CLI.
- The CLI refuses to send a bearer token over plaintext HTTP to a non-loopback
  host.
- `.env` is gitignored; commit only `.env.example`.

## Tests

```bash
# Stellar package (Horizon client, config, validation)
(cd packages/stellar && go test -race ./...)

# Control plane (store, handlers, config)
(cd apps/control-plane && go test -race ./...)

# CLI (control plane client, commands)
(cd apps/cli && go test -race ./...)
```
