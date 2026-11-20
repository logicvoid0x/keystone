# Keystone

*An independent personal project. Not affiliated with, or endorsed by, any company
named Keystone.*

Keystone is an experimental Cosmos SDK chain for borrowing a dollar stablecoin
(KUSD) against native Bitcoin. BTC is never wrapped or bridged: it stays on
Bitcoin, in Taproot outputs controlled by a FROST threshold key held by the
chain's custody set. Prices, Bitcoin chain events and FROST signature shares all
reach the chain through ABCI++ vote extensions. So there is no external oracle,
bridge or custodian; the validators themselves are the trusted party.

> **Status: research prototype.** Keystone has not been audited and is not
> production software. Run it only on Bitcoin regtest or signet. Never point it at
> mainnet or real funds.

## How it works

- **Permissioned proof of stake.** Validators bond the native token `ukey` through
  the stock SDK staking, slashing, distribution, evidence and mint modules. Only
  addresses on the admin's allowlist (`x/admin`) may become validators. There is
  no on-chain governance; an admin multisig is the authority for parameter changes
  and upgrades.
- **Native BTC custody.** Each vault gets its own Taproot deposit address under
  the custody set's group key. The custody set is the top N validators by power,
  and each holds one FROST share. The group key is created by a distributed key
  generation (DKG), with no dealer. When the set changes, a new key is generated
  and the treasury is swept to it. A proven theft of treasury BTC slashes the
  stake of every share holder, and a hard cap limits how much BTC can be custodied.
- **Oracle in vote extensions.** Each validator's price sidecar quotes BTC/USD in
  its vote extension. The chain takes a power-weighted median, keeps a TWAP and
  marks the price stale when quotes stop arriving.
- **Bitcoin in vote extensions.** Each validator runs bitcoind and a watcher that
  reports deposits, confirmations, reorgs and unauthorized spends. Deposits are
  credited only after k confirmations and when more than 2/3 of power agrees.
- **Withdrawals signed in vote extensions.** Withdrawal requests are batched into
  Bitcoin transactions. Custody members contribute FROST nonce commitments and
  signature shares through their vote extensions, and the chain verifies and
  aggregates them. Signers that fail are blamed and excluded on retry, and stuck
  transactions are fee-bumped (RBF).
- **Deterministic liquidations.** The set and order of liquidations are a pure
  function of committed state, so a proposer cannot skip, add or reorder them.
  Liquidated collateral goes to a stability pool first, then to Dutch auctions.
- **Zero gas.** Users need only KUSD. Validators earn `ukey` inflation and a share
  of the stability fees.

The full design, including the threat model, state layout, invariants and failure
modes, is in [`docs/design.md`](docs/design.md).

## Repository layout

| Path | Contents |
| --- | --- |
| `app/` | App wiring, ante handler, invariants, metrics, upgrades |
| `app/protocol/` | ABCI++ handlers: vote extensions, proposal checks, PreBlocker |
| `x/` | Modules: `admin`, `oracle`, `vault`, `stablecoin`, `liquidation`, `custody` |
| `crypto/frost/` | FROST over secp256k1 (Taproot), backed by the Zcash Foundation `frost-secp256k1-tr` crate via cgo |
| `sidecar/` | Off-chain processes inside the node: price fetcher, Bitcoin watcher, FROST signer, DKG participant |
| `cmd/keystoned/` | Node binary and CLI |
| `testutil/` | Single-node and in-process multi-node test harnesses |
| `tools/detlint/` | Static analyzer that enforces determinism in state-machine code |
| `localnet/` | Docker Compose localnet: 4 validators, 4 regtest bitcoinds, mock price feed, Prometheus and Grafana |
| `scripts/` | End-to-end demos, genesis ceremony, release and upgrade-drill scripts |
| `docs/` | Design, operator guide, and the original milestone specs |

## Building

Requirements:

- Go 1.26.10 (64-bit)
- Rust (stable) with the targets `x86_64-unknown-linux-gnu`, `x86_64-pc-windows-gnu` and
  `aarch64-unknown-linux-gnu`
- zig 0.15.2, which is used as the C compiler for cgo on every platform
- `buf` and `jq`
- Docker, for the localnet and demos
- bash (on Windows, Git Bash)

[`mise.toml`](mise.toml) pins Go, zig, buf and jq, so `mise install` sets those up.

```sh
make build        # ./build/keystoned (builds the FROST library first)
make test         # Rust shim tests + go test ./... with invariants enabled
make lint         # go vet + detlint (+ golangci-lint if installed)
```

## Running a localnet

```sh
make localnet-start        # 4 validators + regtest bitcoinds + mock price feed
make localnet-monitoring   # optional: Prometheus + Grafana
make localnet-stop         # keeps state; make localnet-clean wipes it
```

`make e2e` runs the full demo on a fresh localnet. It shows blocks carrying vote
extensions, a BTC deposit credited to a vault, a new validator being allowlisted
and bonding, the custody set rotating to a new key with the treasury swept to it,
and staking rewards with zero gas. Earlier demos (`make e2e-M0` … `make e2e-M5`)
cover deposits, FROST withdrawals, the DKG and failure handling one at a time.

Other useful targets:

| Command | Does |
| --- | --- |
| `make determinism-long` | 4 in-process nodes for 10,000 heights, comparing app hashes |
| `make localnet-byz MODE=…` | Restarts node3 as a misbehaving proposer to show its blocks are rejected |
| `make upgrade-drill` | Rehearses a chain upgrade on the localnet |
| `make release` / `make release-verify` | Reproducible release binaries and checksums |

## Using the chain

The CLI is `keystoned`. Typical user flow:

```sh
keystoned tx vault open-vault --from alice
keystoned query custody deposit-address 1          # send regtest BTC here
keystoned query vault vault 1                      # collateral is credited after k confirmations
keystoned tx vault mint 1 1000000000 --from alice  # mint 1,000 KUSD (amounts in ukusd)
keystoned tx vault repay 1 1000000000 --from alice
keystoned tx vault close-vault 1 <recipient-scriptPubKey-hex> --from alice
keystoned query custody withdrawal <id>            # follow the FROST-signed withdrawal
```

Stability pool and auctions are under `keystoned tx liquidation`. Prices are
under `keystoned query oracle`, and treasury totals under
`keystoned query custody treasury`.

To run a validator, see [`docs/operators.md`](docs/operators.md). It covers
hardware, bitcoind, price sources, key and share backups, monitoring and upgrades.

## Project status

| Area | State |
| --- | --- |
| Oracle, vaults, KUSD, liquidations | Implemented and tested |
| 4-node localnet, determinism and faulty-proposer tests | Implemented and tested |
| Regtest deposits, FROST-signed withdrawals, DKG | Implemented and tested |
| Membership changes, sweeps, retry-and-blame, RBF, monitoring | Implemented and tested |
| Proof of stake with allowlist, top-N custody set, theft slashing | Implemented; long determinism run pending |
| Public signet testnet | Tooling ready (genesis ceremony, releases, operator guide); no live network yet |
| Security audit | Not started. Code marked `FLAG FOR AUDIT` (FROST, DKG, signer, stability pool) needs review first |

## License

Licensed under the [Apache License 2.0](LICENSE).
