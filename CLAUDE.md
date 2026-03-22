# Keystone — instructions for Claude Code

Keystone is a proof-of-authority Cosmos SDK chain. Users lock native BTC in a
Taproot treasury held by the operators' FROST key and mint the KUSD stablecoin
against it. Prices, Bitcoin attestations and FROST signature shares travel in
ABCI++ vote extensions.

## Sources of truth (read before coding)

| File | What it is |
| --- | --- |
| `docs/design.md` | The full design. Section numbers (§5.4 etc.) in specs refer to it. |
| `docs/specs/00-overview.md` | Milestone map, repo layout, conventions, definition of done. |
| `docs/specs/M*.md` | One spec per milestone. Implement one milestone at a time, in order. |
| `docs/verified-apis.md` | SDK / CometBFT / btcd APIs already confirmed against source. Trust these over memory. |
| `docs/decisions.md` | Log of decisions made during implementation. Append, never rewrite. |

If a spec and `docs/design.md` disagree, the spec wins for scope and the design
wins for behaviour. Record the conflict in `docs/decisions.md` and tell the user.

## Versions (pinned)

- Go: toolchain **go1.27.3**. The `go` directive in go.mod must be at least the
  highest directive of any dependency (Cosmos SDK v0.55.0 needs 1.26.5; current
  btcd modules need 1.26.10). Let `go mod tidy` set it; never lower it.
  If a dependency fails to build on 1.27.3, switch the toolchain to go1.26.10,
  record it in `docs/decisions.md`, and continue.
- `github.com/cosmos/cosmos-sdk` **v0.55.0** (pulls `github.com/cometbft/cometbft` **v0.40.0**).
- `cosmossdk.io/collections`, `cosmossdk.io/math`, `cosmossdk.io/log/v2`: versions required by SDK v0.55.0.
- `github.com/btcsuite/btcd`, `btcd/btcec/v2`, `btcd/btcutil`, `btcd/btcutil/psbt`: latest tagged releases.
- Do **not** use the Cosmos Enterprise PoA module (no vote-extension support). We write `x/poa`.

## Hard rules: determinism (state-machine code)

State-machine code is everything under `x/`, `app/protocol/` and `crypto/frost/`, except
`crypto/frost/secret/` and anything under `sidecar/`. In that code:

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
   an event. Only genuine bugs may panic.
7. The `detlint` analyzer (built in M0) enforces 1–4. It must pass in CI.

Sidecar code (price fetcher, Bitcoin watcher, FROST signer) may do I/O and use
randomness; its output only reaches state through vote extensions.

## Never

- Enable BlockSTM (`block-executor = block-stm`) or optimistic execution.
- Add `x/staking`, `x/distribution`, `x/slashing`, `x/mint` or `x/gov`.
- Change a wire format (proto field numbers, VE layout, tx0 layout, store prefixes)
  without updating `docs/design.md` and `docs/decisions.md`.
- Invent an SDK, CometBFT or btcd API. If unsure, read the source in the module
  cache (`go env GOMODCACHE`) or use `go doc`, then add what you confirmed to
  `docs/verified-apis.md`.
- Mark a task done with failing tests or skipped acceptance checks.

## Workflow

1. Work on exactly one milestone spec per session. Start in plan mode: read the
   spec, the design sections it cites, and `docs/verified-apis.md`, then propose a
   plan that maps every spec task to files and tests. Wait for approval.
2. Implement task by task. After each task: `make lint test`, then commit
   (`<milestone>-T<n>: <summary>`).
3. When the milestone's acceptance checks all pass, run the demo script it names,
   update `docs/decisions.md`, and stop. Do not start the next milestone unasked.
4. If the design is ambiguous or wrong, stop and ask. Do not improvise protocol behaviour.

## Commands (create them in M0, keep them working)

| Command | Does |
| --- | --- |
| `make build` | Builds `./build/keystoned` |
| `make test` | `go test ./...` (unit + property tests) |
| `make lint` | `go vet`, `detlint`, `golangci-lint` (if installed) |
| `make proto` | Regenerates protobuf Go code with buf |
| `make localnet-start` / `make localnet-stop` | 4-node localnet (M2+) |
| `make e2e` | End-to-end demo script for the latest finished milestone |

## Style

- Small packages, table-driven tests, `require` from testify.
- Keeper methods take `context.Context`; unwrap with `sdk.UnwrapSDKContext` only where needed.
- Errors wrap with `errorsmod.Wrapf` and module-registered error codes.
- Every exported keeper method has a test. Every invariant in design Appendix A
  gets a property test once the code it covers exists.
