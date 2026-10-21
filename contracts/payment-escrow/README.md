# PaymentEscrow Contract

Minimal Soroban escrow lifecycle for BYOI/Usenova services. Funds are held by
the contract until the consumer releases them to the provider or refunds them.

## Lifecycle

| Entrypoint                             | Auth required | Effect                                    |
| -------------------------------------- | ------------- | ----------------------------------------- |
| `create_escrow(id, consumer, provider, token, amount)` | `consumer` | Locks `amount` of `token` in the contract, status `Pending` |
| `release(id)`                          | `consumer`    | Pays the provider, status `Released`      |
| `refund(id)`                           | `consumer`    | Returns funds to the consumer, status `Refunded` |
| `get_status(id)`                       | none          | Returns the current `EscrowStatus`        |
| `get_escrow(id)`                       | none          | Returns the full escrow record            |

Errors: `NotFound`, `InvalidAmount`, `NotPending`, `AlreadyExists`.

## Build & test

```bash
cd contracts
cargo test                     # unit tests (soroban-sdk testutils)
cargo build --release --target wasm32-unknown-unknown
cargo fmt -- --check
cargo clippy --all-targets -- -D warnings
```

The compiled contract is written to
`contracts/target/wasm32-unknown-unknown/release/payment_escrow.wasm`.

## Manual testnet walkthrough

Requires the [Stellar CLI](https://developers.stellar.org/docs/tools/developer-tools/cli/install-cli)
and network access. This mirrors the automated script in
`scripts/deploy-escrow.sh`.

```bash
cd contracts

# 1. Build
cargo build --release --target wasm32-unknown-unknown
WASM=target/wasm32-unknown-unknown/release/payment_escrow.wasm

# 2. Create and fund a testnet identity for the consumer
stellar keys generate --network testnet consumer

# 3. Deploy the contract
stellar contract deploy \
  --wasm "$WASM" \
  --source consumer \
  --network testnet
# -> export the returned contract id as ESCROW_ID

# 4. Create a test token to escrow (or use the native XLM SAC)
stellar contract asset deploy \
  --asset "USENOVA:$(stellar keys address consumer)" \
  --source consumer --network testnet
# -> export the returned contract id as TOKEN_ID

# 5. Create the escrow (locks funds)
stellar contract invoke --id "$ESCROW_ID" --source consumer --network testnet \
  -- create_escrow \
  --escrow_id 1 \
  --consumer "$(stellar keys address consumer)" \
  --provider "$(stellar keys address provider)" \
  --token "$TOKEN_ID" \
  --amount 400

# 6. Inspect status
stellar contract invoke --id "$ESCROW_ID" --network testnet \
  -- get_status --escrow_id 1

# 7a. Release to provider, or
stellar contract invoke --id "$ESCROW_ID" --source consumer --network testnet \
  -- release --escrow_id 1
# 7b. Refund to consumer
stellar contract invoke --id "$ESCROW_ID" --source consumer --network testnet \
  -- refund --escrow_id 1
```

## Go helper

`packages/stellar` exposes thin helpers that invoke the entrypoints over
Soroban RPC:

```go
client := stellar.NewClient("testnet", escrowContractID)
_, err := client.CreateEscrow(ctx, stellar.EscrowRequest{
    EscrowID: 1,
    Consumer: consumerAddr,
    Provider: providerAddr,
    Token:    tokenContractID,
    Amount:   400,
})
status, err := client.GetEscrowStatus(ctx, 1)
```
