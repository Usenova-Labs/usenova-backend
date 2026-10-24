# Host-on-Usenova Design Note

> What "Host-on-Usenova" means, how it differs from the current BYOI path,
> and what is explicitly out of scope for the MVP.

## TL;DR

| | **BYOI (today)** | **Host-on-Usenova (planned)** |
|---|---|---|
| Who owns the machine | You | Usenova runs the fleet |
| Who installs the agent | You | Usenova provisions it |
| Who pays for compute | You (your cloud bill) | You pay Usenova per deploy |
| Payment primitive | Stellar escrow (in progress) | Stellar escrow |
| Status | Shipped | Planned |

---

## The BYOI path (what exists today)

Bring Your Own Infrastructure is the first and only compute path today. The
user controls every machine. Usenova provides the tooling to run services
reliably on that machine.

```
Your machine                     Usenova-operated
─────────────────────────────    ─────────────────
 usenova-agent (daemon)          control plane
   └─ Docker                        └─ agent registry
   └─ health checks                 └─ command queue
   └─ circuit breaker               └─ Stellar account links
   └─ local API (:9090)
```

**How a deploy works today (BYOI):**

1. You run `nova deploy --name web --image nginx:alpine` on your laptop.
2. The CLI calls the agent's loopback API on your machine.
3. The agent pulls the image and creates a Docker container.
4. The agent health-checks and restarts the container if it crashes.

Or, using the remote command channel:

1. An operator POSTs a `deploy` command to the control plane.
2. The agent polls the control plane (it works behind NAT/firewalls).
3. The agent executes the command and reports the result.

See [docs/getting-started.md](getting-started.md) for the full local walkthrough
and [docs/remote-deploy.md](remote-deploy.md) for the command channel design.

---

## The Host-on-Usenova direction (planned)

In the Host-on-Usenova model, Usenova operates the fleet. Users do not need to
own or manage any servers. They pay per deploy using Stellar.

**Planned flow:**

1. User runs `nova deploy --name web --image nginx:alpine --server cloud`.
2. The CLI tells the control plane to find a free slot in the fleet.
3. The control plane picks an agent it manages, enqueues a deploy command.
4. The Soroban payment-escrow contract locks the user's funds.
5. The agent pulls and starts the container.
6. Once the service is confirmed running, the control plane triggers escrow
   release and pays the infrastructure operator.

```
User                    Usenova Control Plane        Usenova Fleet
────                    ─────────────────────        ─────────────
nova deploy ──────────▶  scheduler                    agent-A
                         payment escrow ──────────▶   agent-B
                         Stellar network              agent-C
```

The payment primitive — the Soroban
[`payment-escrow`](../contracts/payment-escrow/) contract — is already
implemented and tested on Stellar testnet. It handles `create_escrow`,
`release`, and `refund`. Everything else in this section is planned.

---

## Why build Host-on-Usenova on top of BYOI

The BYOI path solves a real problem today (reliable always-on services on
infra you already own) and proves the agent, control plane, and payment
contracts work end-to-end. Host-on-Usenova reuses all of that:

- The same `usenova-agent` binary runs on fleet machines.
- The same command channel (poll-based, NAT-friendly) is how the control
  plane tells fleet agents what to deploy.
- The same Soroban escrow contract handles payment.

Adding a fleet scheduler and a multi-tenant session model on top of the
existing foundation is the delta — not a rewrite.

---

## Non-goals for the MVP

The following are **explicitly out of scope** until after the BYOI MVP is
proven in production:

- **Fleet scheduler** — no bin-packing, no affinity rules, no spot-instance
  integration. Scheduling is a hard problem; the MVP proves the primitives first.
- **Multi-tenant control plane** — the current control plane has a single
  operator token. Per-user accounts and authorization are a follow-up.
- **Mainnet Stellar** — all payment flows use Stellar testnet. Mainnet
  requires an audit of the escrow contract and a formal security review.
- **Pricing models** — dynamic pricing, auctions, and SLA tiers are not
  designed yet. The escrow contract is amount-agnostic and will support
  whatever model is chosen.
- **SLA enforcement on-chain** — the escrow contract today requires the
  consumer to authorize the release. Automated, metric-driven release is a
  future contract upgrade.
- **Public agent registration** — today any agent can register with a token.
  A public fleet requires a vetted allowlist and operator identity proofs.

---

## Payment primitive: the Soroban escrow contract

The [`contracts/payment-escrow/`](../contracts/payment-escrow/) directory
contains the Soroban smart contract that underlies all payment flows.

| Entrypoint | Effect |
|---|---|
| `create_escrow(id, consumer, provider, token, amount)` | Locks funds in the contract |
| `release(id)` | Pays the provider (service delivered) |
| `refund(id)` | Returns funds to the consumer (service not delivered) |
| `get_status(id)` | Returns `Pending`, `Released`, or `Refunded` |

The contract is tested on Stellar testnet. See
[`contracts/payment-escrow/README.md`](../contracts/payment-escrow/README.md)
for build, test, and manual testnet walkthrough instructions.

The Go client in [`packages/stellar/`](../packages/stellar/) wraps the
contract entrypoints and the Horizon balance API.

---

## Current focus

The project is actively working on:

1. Proving the BYOI path end-to-end (agent, CLI, control plane, Docker).
2. Stellar testnet integration (account linking, balance queries, escrow).
3. Hardening: session auth, circuit breaker, command channel reliability.

Host-on-Usenova work will begin once the BYOI path is stable and the escrow
contract has been reviewed. Contributions to the items above are the fastest
path to enabling the fleet direction.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for how to pick up an issue, and
[GitHub Issues](https://github.com/Usenova-Labs/usenova-backend/issues) for
the current work queue.
