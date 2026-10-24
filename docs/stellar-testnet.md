# Stellar Testnet Setup

> How to link a Stellar account and query its balance on testnet.

**Status**: The control plane exposes account-link and balance endpoints and
the CLI has matching `nova stellar` commands. This document covers testnet
only; mainnet is out of scope for the MVP.

## Pre-flight checklist

Before you begin, make sure the following are ready:

- [ ] **Stellar CLI installed** - [Install the Stellar CLI](https://developers.stellar.org/docs/tools/developer-tools/cli/install-cli) and verify with `stellar --version`.
- [ ] **Stellar testnet account generated** - You can create one with the Stellar CLI (or an existing testnet keypair). Never use a mainnet key.
- [ ] **Testnet account funded** - Fund your account via [Stellar Friendbot](https://developers.stellar.org/docs/tutorials/create-account) (rate limits apply, see troubleshooting).
- [ ] **Control plane built and running** - Build with `./scripts/build.sh` and run `./bin/nova-control` (or your preferred method).
- [ ] **CLI built** - `./bin/nova` available (built by the build script).
- [ ] **Network access** - Reachable to `https://horizon-testnet.stellar.org` (and `https://friendbot.stellar.org`).
- [ ] **Environment configured** - `NOVA_CONTROL_PLANE_URL` set (defaults to `http://127.0.0.1:8080`). See [Configuration](#configuration) for details.

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
| `NOVA_CONTROL_PLANE_URL` | Control plane URL used by the CLI (falls back to `USENOVA_CONTROL_PLANE_URL`) | `http://127.0.0.1:8080` |

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

**Terminal capture (example):**

```text
$ stellar keys generate --network testnet demo
Key demo added to `~/.config/stellar/keys/demo.toml`

$ ADDRESS=$(stellar keys address demo)
$ echo "$ADDRESS"
GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX

$ curl "https://friendbot.stellar.org/?addr=$ADDRESS"
{"successful":true,"hashes":["..."]}
```

You can also paste any existing testnet public key (`G...`). Never use a
mainnet or valuable key here.

## Link the account

Start the control plane, then link the address with the CLI:

```bash
# terminal 1
./bin/nova-control

# terminal 2
export NOVA_CONTROL_PLANE_URL=http://127.0.0.1:8080
./bin/nova stellar link "$ADDRESS" --label payouts
./bin/nova stellar accounts
```

**Terminal capture (example):**

```text
$ ./bin/nova stellar link GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX --label payouts
Linked GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX (network: testnet)
Label: payouts

$ ./bin/nova stellar accounts
ADDRESS                                                         NETWORK  LABEL    LINKED
GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX       testnet  payouts  2026-10-24T12:00:00Z
```

Or call the API directly:

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/stellar/accounts \
  -H 'Content-Type: application/json' \
  -d "{\"address\":\"$ADDRESS\",\"label\":\"payouts\"}"
```

**Terminal capture (example):**

```text
$ curl -s -X POST http://127.0.0.1:8080/api/v1/stellar/accounts \
  -H 'Content-Type: application/json' \
  -d "{\"address\":\"$ADDRESS\",\"label\":\"payouts\"}"
{"account":{"address":"GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX","network":"testnet","label":"payouts","linked_at":"2026-10-24T12:00:00Z"},"message":"Account linked successfully"}
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

**Terminal capture (example):**

```text
$ ./bin/nova stellar balance "$ADDRESS"
Address: GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
Network: testnet
ASSET  ISSUER  AMOUNT
XLM    -       10000.0000000

$ ./bin/nova stellar balance "$ADDRESS" -o json
{"address":"GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX","network":"testnet","balances":[{"asset":"XLM","amount":"10000.0000000"}]}
```

Direct API equivalent:

```bash
curl -s "http://127.0.0.1:8080/api/v1/stellar/accounts/$ADDRESS/balance"
curl -s "http://127.0.0.1:8080/api/v1/stellar/accounts/$ADDRESS/balance?asset=USDC"
```

**Terminal capture (example):**

```text
$ curl -s "http://127.0.0.1:8080/api/v1/stellar/accounts/$ADDRESS/balance"
{"address":"GXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX","network":"testnet","balances":[{"asset":"XLM","amount":"10000.0000000"}]}
```

Amounts are returned as strings exactly as Horizon reports them (7 decimal
places); the CLI does not parse them into floats, so no precision is lost.

An existing account with no balance matching the requested `?asset=` filter
returns `200 OK` with an empty `balances` list (`nova` prints
`No balances found.`). A 404 means the account itself was not found on the
configured network; see [Unfunded account](#unfunded-account) and
[Wrong network](#wrong-network).

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

## Troubleshooting

### Friendbot rate limits

Friendbot is shared infrastructure and applies rate limits per IP and per
account. If funding fails with `429 Too Many Requests` or a JSON error such as
`"error": "rate limit exceeded"`:

- Wait a few minutes before retrying.
- Reuse an already-funded testnet account instead of generating a new one.
- Do not script repeated funding calls; fund each account once.
- Confirm the address is correct before retrying so you do not waste requests.

```text
$ curl "https://friendbot.stellar.org/?addr=$ADDRESS"
{"type":"...","title":"Rate Limit Exceeded","status":429,"detail":"..."}
```

### Wrong network

If the control plane or CLI points at mainnet while you are using a testnet
account (or vice versa), balance queries return `404 Not Found` because the
account does not exist on the configured network. An empty account response is
reserved for an existing account with no matching `?asset=` balance; a
wrong-network account follows the 404 path instead.

- Check `STELLAR_NETWORK` (`testnet` is the default for local development).
- If you override `STELLAR_HORIZON_URL`, make sure it is the testnet Horizon:
  `https://horizon-testnet.stellar.org`.
- Restart the control plane after changing environment variables.
- Verify the address on a public explorer or with the Stellar CLI before
  assuming the account is missing.

```bash
echo "$STELLAR_NETWORK"
echo "$STELLAR_HORIZON_URL"
curl -s "https://horizon-testnet.stellar.org/accounts/$ADDRESS" | head
```

### Unfunded account

A Stellar account does not exist on ledger until it is funded with the minimum
reserve. Querying an unfunded account returns `404` (account not found), not a
zero balance.

- Fund the account with Friendbot (testnet only) and retry.
- Check that the address starts with `G` and is 56 characters long.
- If funding succeeded, allow a few seconds for Horizon to catch up.

```text
$ ./bin/nova stellar balance "$ADDRESS"
Error: control plane error (404): Account not found on network
```

### Account already linked / invalid address

- Linking the same address twice may return a conflict or update the existing
  label depending on your control plane version; list accounts with
  `./bin/nova stellar accounts` before linking again.
- An `Invalid Stellar address` error means the value is not a 56-character
  `G...` public key. Secret keys (`S...`) are deliberately rejected and must
  never be submitted.

### Control plane unreachable

- Confirm the process is running and listening on the expected port.
- Check `NOVA_CONTROL_PLANE_URL` (and the `USENOVA_CONTROL_PLANE_URL` fallback).
- The CLI refuses to send a bearer token over plaintext HTTP to a non-loopback
  host; use HTTPS or a loopback address for local development.

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
