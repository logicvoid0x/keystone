# M0 — Scaffold

**Goal:** a single-node Keystone chain that starts, produces blocks, injects tx0
from block 2, and has a working `x/poa`. No economics yet.

**Design sections:** §4.1–4.5, §4.2 (x/poa spec), §5.2–5.9, §10.1, §10.3, §10.4, §11.

**Estimate:** 2–3 hours.

## Tasks

### T1 — Module, dependencies, build

- `go mod init github.com/OWNER/keystone` (keep `OWNER` as a placeholder the user will replace).
- Require `github.com/cosmos/cosmos-sdk v0.55.0`; let it pull CometBFT v0.40.0.
- `go.mod`: `toolchain go1.27.3`; `go` directive as set by `go mod tidy` (at least 1.26.5).
- Copy the SDK v0.55.0 `replace` directives into our go.mod:
  `github.com/99designs/keyring => github.com/cosmos/keyring v1.2.0` and
  `github.com/syndtr/goleveldb => github.com/syndtr/goleveldb v1.0.1-0.20210819022825-2ae1ddf74ef7`.
  (Do not copy simapp's `cosmos-sdk => ../.` replace.)
- `Makefile` targets from CLAUDE.md (`build`, `test`, `lint`, `proto`; stubs for the rest).
- `buf.yaml`, `buf.gen.gogo.yaml` modelled on SDK v0.55.0's `proto/` setup; `make proto` regenerates code.

### T2 — `tools/detlint` determinism analyzer

A `golang.org/x/tools/go/analysis` analyzer plus a `cmd` wrapper run by `make lint`.

Applies to packages whose import path contains `/x/`, `/app/protocol` or `/crypto/frost`,
excluding `/crypto/frost/secret`, `/sidecar/` and `_test.go` files. Reports:

1. any expression or declaration of type `float32` or `float64`;
2. calls to `time.Now`, `time.Since`, `time.Until`;
3. `range` over a value whose underlying type is a map;
4. `go` statements;
5. imports of `math/rand`, `math/rand/v2`, `crypto/rand`, `net`, `net/http`, `os/exec`.

A line may opt out with a trailing `//detlint:allow <reason>`; the reason is required.
Tests use `analysistest` with one positive and one negative case per rule.

### T3 — `x/poa` (complete, per design §4.2)

Proto `keystone.poa.v1`:

```protobuf
message Params   { string admin = 1; }
message Operator { string operator_address = 1; bytes consensus_pubkey = 2; string moniker = 3; int64 power = 4; }
message GenesisState { Params params = 1; repeated Operator operators = 2; }
message ValidatorEntry { string operator_address = 1; bytes consensus_pubkey = 2; string moniker = 3; int64 power = 4; }
message MsgSetValidators { string admin = 1; repeated ValidatorEntry entries = 2; }  // signer: admin
message MsgUpdateParams  { string admin = 1; Params params = 2; }                    // signer: admin
// Query: Params, Validators (all operators, sorted by operator address), Validator{address}, TotalPower
```

`consensus_pubkey` is the raw 32-byte ed25519 key.

Keeper state (`collections`, prefixes from design §10.1): params `0x01`, operators `0x02`,
cons-address index `0x03`, used pubkeys `0x04`, last power sent to CometBFT `0x05`.

Rules for `MsgSetValidators`:

- signer equals `params.admin`; power is 0 or 1;
- a new operator must bring a consensus pubkey never used before (check `0x04`);
- an existing operator's pubkey cannot change (no rotation in v1);
- before applying, call `RemovalGuard.AllowSetValidators(ctx, before, after) error`.
  The guard interface is defined in `x/poa/types`; the default implementation allows
  everything. `x/custody` provides the real guard in M5;
- records are never deleted, only set to power 0.

Keeper methods:

```go
// baseapp.ValidatorStore (see docs/verified-apis.md)
GetPubKeyByConsAddr(ctx context.Context, addr sdk.ConsAddress) (cmtprotocrypto.PublicKey, error)
ActiveValidators(ctx context.Context) ([]types.Operator, error) // power > 0, sorted by operator address
TotalPower(ctx context.Context) (int64, error)
Admin(ctx context.Context) (sdk.AccAddress, error)
// x/genutil StakingKeeper (see docs/verified-apis.md)
ApplyAndReturnValidatorSetUpdates(ctx context.Context) ([]abci.ValidatorUpdate, error)
```

`GetPubKeyByConsAddr` must succeed for operators at power 0 too, so VEs from the last
heights before a removal still verify.

EndBlocker: compare each operator's power with `0x05`; return `abci.ValidatorUpdate`s
for differences in a deterministic order (sorted by consensus address); update `0x05`.
InitGenesis returns the initial validator set the same way.

Events: `validator_set_updated{added, removed}`, `admin_changed{old, new}`.

Tests: every rule above, EndBlocker diff output, genesis round trip, `GetPubKeyByConsAddr`
for an operator removed in the same block.

### T4 — Skeletons for the other five modules

`x/oracle`, `x/vault`, `x/stablecoin`, `x/liquidation`, `x/custody`, each with:

- proto `Params` holding **every** parameter in design §10.3 with its default value,
  `GenesisState{params}`, `MsgUpdateParams` (signer must equal the x/poa admin), `Query/Params`;
- keeper with collections for params only (other state comes in later milestones);
- `Params.Validate()` with the bounds in design §17;
- `module.go` (AppModule, genesis, services, autocli).

### T5 — App wiring (`app/app.go`)

Start from SDK v0.55.0 `simapp/app.go` and remove staking, distribution, slashing, mint,
gov, evidence, feegrant, authz, epochs and params. Keep auth, bank, consensus, upgrade,
genutil. Add x/poa and the five Keystone modules.

- genutil gets x/poa's keeper as its `StakingKeeper`. Confirm genutil returns no validator
  updates when there are no gentxs, so x/poa is the only module returning them.
- `x/upgrade` authority = x/poa admin address. (The admin is read from genesis
  before app construction, or the upgrade authority is resolved at runtime; record which
  in `docs/decisions.md`.)
- Module accounts (created now, used in M1): `stablecoin` (minter, burner), `surplus`,
  `liquidation` (burner).
- InitGenesis order: auth, bank, poa, oracle, stablecoin, vault, liquidation, custody, genutil, upgrade, consensus.
- EndBlocker order: stablecoin, custody, poa.
- PreBlocker: call the module manager's PreBlock (x/upgrade) first, then `protocol.PreBlock` (T6).
- Register `SetPrepareProposal`, `SetProcessProposal`, `SetExtendVoteHandler`,
  `SetVerifyVoteExtensionHandler` with the handlers from T6.
- Ante handler: standard SDK v0.55 ante chain, zero minimum gas price, plus a
  `MaxTxPerAccountPerBlock` decorator (limit 5, counter in a transient store keyed by signer).
- Do not call `SetOptimisticExecution`. Keep `block-executor = "sequential"`.

### T6 — `app/protocol`, ABCI++ handlers

Follow design §5 exactly.

- `ve.proto` (`keystone.ve.v1`): `VoteExtension{version, height, oracle, bitcoin, frost}` with
  the full sub-messages from design §6.1, §9.7 and §9.11, even though they stay empty in M0.
  Canonical form: repeated fields sorted as the design states; `IsCanonical()` and
  `WithinCounts()` checks; `MaxVEBytes = 16384`.
- `ExtendVote`: returns `VoteExtension{version: 1, height: req.Height}` (sidecars come later).
  Never returns an error.
- `VerifyVoteExtension`: structure-only checks from §5.2.
- `inject.go`: tx0 = `"KSTN" | 0x01 | proto(InjectedVotes{extended_commit})`. `hasMagic`,
  `encodeInjected`, `decodeInjected`, and `matchesLastCommit` with the rules listed in
  `docs/verified-apis.md` (copied from the SDK's unexported check).
- `verify.go`: `VerifyVotes` exactly as design §5.4, using the sign bytes in
  `docs/verified-apis.md`. Height argument is `req.Height - 1`.
- `proposal.go`: PrepareProposal and ProcessProposal exactly as design §5.7 and §5.8.
  `veActive(height)` is true when `ABCI.VoteExtensionsEnableHeight != 0 && height > enableHeight`.
- `preblock.go`: decode tx0 (when `veActive`), run `VerifyVotes`, call an `Aggregate`
  stub, then the module steps (stubs in M0). Totality rule from §5.9.

### T7 — CLI and genesis tooling

- `keystoned` root command from the SDK v0.55.0 simapp pattern.
- `keystoned testnet init-files --operators N --output DIR --chain-id keystone-local-1`:
  generates N node homes, ed25519 consensus keys, an operator account per node, an admin
  multisig account (threshold `floor(2N/3)+1`; for N = 1 a single key), and a shared
  genesis with:
  - x/poa operators at power 1, admin = the multisig;
  - consensus params `ABCI.VoteExtensionsEnableHeight = 1`, `Block.MaxBytes = 4194304`,
    `Block.MaxGas = 100000000`, validator pubkey types `["ed25519"]`;
  - a funded test account per node (bank balances are not needed for fees, which are zero).
- `app.toml`: `minimum-gas-prices = "0ukusd"`, `block-executor = "sequential"`.
- Target ~3 s blocks: find where CometBFT v0.40 sets the commit timeout (config.toml or
  consensus params), set it, and record the location in `docs/verified-apis.md`.

### T8 — Tests

- `vecodec`: canonical / non-canonical, oversize, wrong version, wrong height.
- `inject`: round trip; `matchesLastCommit` table (round mismatch, vote count mismatch,
  unsorted, duplicate, address mismatch, power mismatch, valid).
- `VerifyVotes`: build an `ExtendedCommitInfo` signed with 4 test ed25519 keys:
  all valid; one bad signature (ignored, still > 2/3); two bad (≤ 2/3);
  absent votes; unknown validator key.
- Prepare/Process: Prepare output always passes Process; Process rejects missing tx0,
  tx0 at index 1, an extra magic tx, a tampered vote list, and ≤ 2/3 verified power.

## Acceptance checks

1. `make build lint test` passes from a clean clone.
2. `scripts/demo-M0.sh`:
   - creates a 1-node home with `testnet init-files --operators 1`, starts it, waits for height 20;
   - via CometBFT RPC `/block?height=1` confirms block 1 has no tx starting with `KSTN`;
   - for heights 2–20 confirms `txs[0]` decodes as tx0 and no other tx has the magic;
   - `keystoned q poa validators` lists 1 operator at power 1;
   - stops the node.
3. An in-process app test shows `MsgSetValidators` from the admin adding a second operator
   makes EndBlocker return a `ValidatorUpdate` with power 1, and a non-admin signer is rejected.
