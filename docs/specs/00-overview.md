# Implementation overview

Build Keystone in the milestone order below. Each milestone ends in something
you can demo and leaves every earlier test passing. M0–M2 are the short demo;
M3–M6 add real Bitcoin custody.

| Milestone | Spec | Ends with | Design sections |
| --- | --- | --- | --- |
| M0 Scaffold | `M0-scaffold.md` | Single node producing blocks with tx0 from block 2; `x/poa` working | §4, §5, §10.4, §4.2 x/poa |
| M1 Single-node slice | `M1-oracle-vaults-liquidations.md` | Mock price drop liquidates a vault in the next block | §5, §6, §7, §8 |
| M2 Localnet | `M2-localnet-determinism.md` | 4 nodes, 10,000-block app-hash run, faulty proposers rejected | §5.4, §15 |
| M3 Regtest deposits | `M3-regtest-deposits.md` | regtest BTC credited to a vault after k confirmations | §9.6, §9.7, §3.2 |
| M4a FROST signing | `M4a-frost-signing.md` | Withdrawal signed by FROST in VEs and confirmed on regtest | §9.8, §9.9, §9.11 |
| M4b DKG | `M4b-dkg.md` | Fresh localnet derives its group key with no dealer | §9.3 |
| M5 Robustness | `M5-robustness.md` | Operator removed, funds swept to new key; signer killed mid-session | §9.4, §9.10, §12 |
| M6 Signet testnet | `M6-signet.md` | 7 operators on signet, public end-to-end flow | §16 |
| M7 Proof of stake | `M7-pos.md` | Stock SDK staking with an admin allowlist; top-N custody set; theft slashes the custody set | §3, §4.2, §9.2, §9.4, §9.12, §20 |

## Repository layout

```
keystone/
  CLAUDE.md
  Makefile
  go.mod                       # module github.com/OWNER/keystone  (replace OWNER)
  cmd/keystoned/               # main, root command, init/testnet commands
  app/
    app.go                     # module wiring, keepers, ABCI handler registration
    genesis.go
    protocol/                  # ABCI++ logic (state machine, deterministic)
      vecodec.go               # VE envelope encode/decode/canonical checks
      inject.go                # tx0 encode/decode, KSTN magic, matchesLastCommit
      verify.go                # VerifyVotes (§5.4)
      aggregate.go             # Aggregate: oracle, bitcoin, frost sub-payloads
      proposal.go              # PrepareProposal, ProcessProposal
      voteext.go               # ExtendVote, VerifyVoteExtension
      preblock.go              # PreBlocker orchestrator (§5.9)
  sidecar/                     # NOT state machine; may do I/O
    price/                     # price fetcher + mock feed server
    btcwatch/                  # Bitcoin watcher (M3)
    signer/                    # FROST signer + nonce store (M4a)
  crypto/frost/                # FROST math, deterministic parts usable on-chain (M4a)
  crypto/frost/internal/zf/    # cgo bridge + Rust shim over the ZF frost crates (M7a)
  x/admin/ (x/poa before M7)  x/oracle/  x/vault/  x/stablecoin/  x/liquidation/  x/custody/
    keeper/  types/  module.go  genesis.go
  proto/keystone/<module>/v1/*.proto
  tools/detlint/               # determinism analyzer (M0)
  testutil/                    # in-process multi-node harness (M2)
  localnet/                    # docker-compose, scripts (M2+)
  scripts/                     # e2e demo scripts
  docs/
```

## Conventions

- Proto packages: `keystone.<module>.v1`; Go packages generated into `x/<module>/types`.
  Amount fields use `string` with `(cosmos_proto.scalar) = "cosmos.Int"` and
  `(gogoproto.customtype) = "cosmossdk.io/math.Int"`, `(gogoproto.nullable) = false`.
- Store: `cosmossdk.io/collections` with the prefixes in design §10.1. Prefix bytes are
  constants in `types/keys.go`, never reused.
- Params: per-module `Params` message, `Validate()` enforces the bounds in design §17.
  Authority for every `MsgUpdateParams` is the admin address read from `x/admin` params (`x/poa` before M7).
- Units: KUSD in `ukusd` (1e-6), BTC in satoshis, prices in micro-USD per BTC (`uint64`
  on the wire, `sdkmath.Int` in state), ratios in basis points.
- Events: names and attributes exactly as design §11.3.
- Errors: each module registers codes in `types/errors.go`.

## Definition of done (every milestone)

1. Every task in the spec is implemented with its tests.
2. `make lint test` passes, including `detlint`.
3. The acceptance checks listed in the spec pass, run from a clean checkout.
4. The milestone's demo script (`scripts/demo-<milestone>.sh`) runs end to end.
5. `docs/decisions.md` lists any deviation from the design, with the reason.
6. Nothing outside the milestone's scope was started.

## Prompt to run the milestones in Claude Code

Milestones run autonomously, one after another, per the Workflow section of `CLAUDE.md`:

```
Follow CLAUDE.md. Complete every remaining milestone in docs/status.md in order,
without waiting for approval. When in doubt, make a conservative default assumption,
update docs/design.md and log it in docs/decisions.md.
```

Or use the slash command `/milestone [SPEC]` (defined in `.claude/skills/milestone/SKILL.md`).
