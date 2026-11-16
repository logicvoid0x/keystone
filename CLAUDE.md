# Keystone — notes for Claude Code

Keystone is a permissioned proof-of-stake Cosmos SDK chain. Users lock native BTC
in a Taproot treasury held by the custody set's FROST key and mint the KUSD
stablecoin against it. Prices, Bitcoin attestations and FROST signature shares
travel in ABCI++ vote extensions (VEs) and are applied in the PreBlocker.

## Docs

| File | What it is |
| --- | --- |
| `docs/design.md` | The full design. Section references (§5.9 etc.) in code comments point here. It is the source of truth for behaviour. |
| `docs/operators.md` | Running a node: hardware, bitcoind, price sources, keys, backups, alerts, upgrades. |
| `docs/specs/*.md` | The original milestone specs (M0–M7a). Historical; useful for why code is shaped as it is. |
| `docs/decisions.md`, `docs/status.md`, `docs/verified-apis.md` | Local only (in `.git/info/exclude`, not committed). Decision log, milestone status, and SDK / CometBFT / btcd APIs confirmed against source. Use them if present. |

## Architecture

```
cmd/keystoned/        root, start, genesis ceremony, custody init/shares, testnet init-files/add-node,
                      decode-tx0, tx admin-upgrade
cmd/mockfeed/         mock price feed used by the demos and localnet
app/                  app.go wiring, ante (zero gas), genesis/export, invariants (build tag
                      `invariants`), metrics, upgrades (v2), byzantine proposer (build tag `byzantine`)
app/protocol/         ABCI++ (state machine): VE codec, tx0 injection, VerifyVotes, Aggregate,
                      Prepare/ProcessProposal, ExtendVote/VerifyVoteExtension, PreBlocker, VE version schedule
app/valset/           adapts x/staking to the validator view x/oracle and x/custody read
x/admin/              admin address (authority for every MsgUpdateParams) + validator allowlist
x/oracle/             VE price aggregation, liquidation price, staleness
x/vault/              BTC-collateral vaults, KUSD debt, interest drip, withdraw/close requests
x/stablecoin/         KUSD (ukusd) mint/burn, surplus
x/liquidation/        selection, stability pool (Liquity P/S port, flagged for audit), auctions
x/custody/            Taproot deposit scripts, key epochs, deposits/UTXOs, withdrawal batches,
                      FROST signing sessions, nonce pools, DKG, membership change + sweeps,
                      RBF, consolidation, slashing of the custody set
x/validators/         validator record shared by oracle/custody (no x/staking import)
x/internal/bounds/    param bounds (design §17)
crypto/frost/         FROST (RFC 9591, secp256k1-tr) public operations used on-chain, DKG checks, DLEQ
crypto/frost/secret/  nonce generation, signing, dealer keygen — sidecar only (detlint bans it in state machine)
crypto/frost/internal/zf/  cgo bridge + Rust shim over the Zcash Foundation frost crates
sidecar/price/        price fetcher → OraclePayload
sidecar/btcwatch/     bitcoind RPC + ZMQ watcher → BitcoinPayload; broadcasts signed txs
sidecar/signer/       FROST signer → FrostPayload; bbolt nonce store (sidecar/signer/noncestore)
sidecar/dkg/          DKG participant (signs MsgDKGCommit/Deal/Complaint with the custody hot key)
testutil/             apptest (single-node), multinode (in-process N-node harness, determinism,
                      byzantine, DKG, failures), btcfake, devdealer, fakebank
tools/detlint/        determinism analyzer
localnet/             docker-compose: 4 nodes, 4 regtest bitcoinds, mockfeed, Prometheus/Grafana, nginx example
scripts/              demo-M*.sh, add-validator.sh, genesis-ceremony.sh, release.sh, upgrade-drill.sh
proto/keystone/<module>/v1/   protobuf; Go generated into x/<module>/types (VE types in app/protocol/vetypes)
```

**Block flow.** ExtendVote collects the sidecars' payloads into a VE. The next
proposer injects the previous commit's VEs as tx0 (`KSTN` magic); ProcessProposal
rejects a block whose tx0 does not match. The PreBlocker then runs, each step in a
cached context: Aggregate → `oracle.ApplyAggregate` → `vault.Drip` →
`liquidation.Select` → `liquidation.Execute` → `custody.BuildOps` → `custody.ApplyOps`.

**Consensus.** Stock SDK `x/staking`, `x/slashing`, `x/distribution`, `x/evidence`
and `x/mint` (bond denom `ukey`). `x/admin` gates who may become a validator. The
custody set is the top N validators by power.

## Versions (pinned)

- Go **1.26.10** (`.go-version`, `mise.toml`, `GOTOOLCHAIN=local`). Use a 64-bit Go;
  `make check-go` rejects 32-bit. Let `go mod tidy` set the `go` directive; never lower it.
- `github.com/cosmos/cosmos-sdk` **v0.55.0**, `github.com/cometbft/cometbft` **v0.40.0**.
- btcd split modules (`btcec/v2`, `txscript/v2`, `wire/v2`, `chainhash/v2`, `chaincfg/v2`, `address/v2`).
- FROST: Zcash Foundation `frost-core` / `frost-secp256k1-tr` **=3.0.0** (Rust, `Cargo.lock`
  committed) in `crypto/frost/internal/zf/rust`, linked with cgo. Needs Rust with targets
  `x86_64-pc-windows-gnu`, `x86_64-unknown-linux-gnu`, `aarch64-unknown-linux-gnu`, and
  zig **0.15.2**, which the Makefile uses as the C compiler for every target.
- `buf` 1.75.0, `jq` (via `mise install`). Docker for the localnet.

## Commands

| Command | Does |
| --- | --- |
| `make build` | Builds the FROST library (`make zf-lib`), then `./build/keystoned` |
| `make build-dev` | `keystoned` + `mockfeed` |
| `make test` | `cargo test` of the shim, `go test -tags invariants ./...`, byzantine proposer test |
| `make lint` | `go vet` (default, `invariants`, `byzantine` tags), `detlint`, `golangci-lint` if installed |
| `make proto` / `make proto-lint` | Regenerate protobuf Go code / buf lint |
| `make determinism-long` | 4 nodes, 10,000 heights app-hash comparison (~10 min; use `GOFLAGS=-p=2`) |
| `make localnet-start` / `-stop` / `-clean` | Docker localnet (`DEALER=` for a dealerless DKG start) |
| `make localnet-byz MODE=…` | Restart node3 as a byzantine proposer (`drop_tx0`, `extra_magic`, `corrupt_one_ve`, `corrupt_two_ve`) |
| `make localnet-monitoring` | Prometheus (with alerts) + Grafana |
| `make e2e` | Latest demo (`scripts/demo-M7.sh`); `make e2e-M0` … `e2e-M5` for earlier ones |
| `make upgrade-drill` | v2 upgrade rehearsal on the localnet |
| `make release` / `release-verify` | Reproducible binaries + SHA256SUMS / rebuild-and-compare |

On Windows run tests through `scripts/gotest.sh` (as `make test` does); it works
around antivirus and Smart App Control failures on test binaries.

## Hard rules: determinism (state-machine code)

State-machine code is everything under `x/`, `app/protocol/` and `crypto/frost/`, except
`crypto/frost/secret/`. Nothing under `sidecar/` is state-machine code. The public entry
points of the Rust shim (`crypto/frost/internal/zf/rust`) are state-machine code too: they
must stay pure functions of their inputs (no randomness, no I/O, `BTreeMap` only), catch
every panic, and detlint cannot check them. In state-machine code:

1. No `float32` / `float64`. Amounts are `sdkmath.Int`; ratios are WAD (1e18) or
   RAY (1e27) fixed point. Rounding always favours the protocol (debt up, payouts down).
2. No `time.Now()`. Use `ctx.BlockTime()` / `ctx.HeaderInfo().Time`.
3. No `range` over Go maps when the order can affect output. Iterate
   `collections` (ordered) or sort slices with a total order (tie-break on bytes).
4. No goroutines, no `math/rand`, no `crypto/rand`, no network or file I/O.
5. Protobuf messages have no `map` fields; repeated fields are kept sorted and
   checked on decode.
6. **PreBlocker totality (§5.9):** no PreBlocker step may return an error for a tx0
   that ProcessProposal accepted. If an operation cannot apply, skip it and emit
   an event (`protocol_step_failed`). Only genuine bugs may panic.
7. `detlint` enforces 1–4 and must pass.

Sidecars may do I/O and use randomness; their output reaches state only through
vote extensions, where more than 2/3 of power must agree (prices, Bitcoin) or every
share is verified (FROST).

## Never

- Enable BlockSTM (`block-executor = block-stm`) or optimistic execution.
- Add `x/gov` (the admin multisig stays the authority; ADR-018).
- Allow `MsgRotateConsPubKey` (rotation makes VEs unverifiable; ADR-015).
- Charge gas: the minimum gas price stays zero (ADR-019).
- Change a wire format (proto field numbers, VE layout, tx0 layout, store prefixes)
  without updating `docs/design.md`. Store prefix bytes live in `types/keys.go` and are never reused.
- Invent an SDK, CometBFT or btcd API. If unsure, read the source in the module
  cache (`go env GOMODCACHE`) or use `go doc`.
- Weaken or skip a test to make it pass.

## Conventions

- Proto packages `keystone.<module>.v1`. Amounts are `string` with
  `(cosmos_proto.scalar) = "cosmos.Int"`, `(gogoproto.customtype) = "cosmossdk.io/math.Int"`,
  `(gogoproto.nullable) = false`.
- State in `cosmossdk.io/collections` with the prefixes in design §10.1.
- Params: per-module `Params`; `Validate()` enforces the design §17 bounds (`x/internal/bounds`).
  Authority for every `MsgUpdateParams` is the admin address from `x/admin`.
- Units: KUSD in `ukusd` (1e-6), BTC in satoshis, prices in micro-USD per BTC (`uint64`
  on the wire, `sdkmath.Int` in state), ratios in basis points.
- Events: names and attributes as in design §11.3. Errors: registered per module in
  `types/errors.go`, wrapped with `errorsmod.Wrapf`.
- Keeper methods take `context.Context`; unwrap with `sdk.UnwrapSDKContext` only where needed.
- Small packages, table-driven tests, `require` from testify. Every exported keeper
  method has a test; every invariant in design Appendix A has a property test.
- Code marked `FLAG FOR AUDIT` (FROST, DKG, signer, stability pool) is security-critical:
  change it only with tests and say so in the summary.
- If the design is ambiguous or wrong, stop and ask. Do not improvise protocol behaviour.
- Before calling work done: `make lint test`. For changes to `app/protocol`, the PreBlocker
  or any keeper the PreBlocker calls, also run the multinode determinism tests.

## Current state

M0–M5 and M7a are complete. M6 (signet) engineering is done; its live-network
acceptance needs external operators and funding. M7 (proof of stake) is done apart
from the 10k-height determinism run. Open: audit of the FLAG FOR AUDIT code,
`golangci-lint` not set up.
