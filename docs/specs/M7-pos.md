# M7 — Proof of stake

**Goal:** replace the custom proof-of-authority module with Cosmos-native proof of stake.
Validators bond a new native token through the stock SDK modules; an admin allowlist
decides who may create a validator; the top N bonded validators hold the FROST shares of
the BTC treasury; a proven theft of treasury BTC slashes every share holder. Users still
pay zero gas and need only KUSD.

**Design sections:** §3.1, §3.2, §4.2 (x/admin, staking wiring), §5.3, §7.7, §9.1, §9.2,
§9.4, §9.12, §10.1, §10.4, §12, §17, §20 (ADR 016–019), Appendix A.

**No network is live** (M6 is blocked), so M7 ships as a fresh genesis: no state migration.

**Estimate:** 3–4 days.

## Tasks

### T0 — Record the decision

CLAUDE.md (Never list), design sections above, ADRs 016–019 superseding 007, 013 and 014,
`docs/decisions.md`, this spec, `00-overview.md`, `status.md`.

### T1 — Native token and SDK staking wiring

- Add `x/staking`, `x/slashing`, `x/distribution`, `x/evidence` and `x/mint` (SDK v0.55.0)
  to `app/app.go`. Bond denom `ukey`. Staking's EndBlocker is the only source of
  validator updates; genesis validators are written into staking genesis (design §10.4).
- Every SDK keeper's authority is the admin module address; their `MsgUpdateParams` are
  re-gated to the current admin as `app/authority.go` already does for upgrade and consensus.
- `app/protocol` uses the staking keeper as its `ValidatorStore`.
- An ante decorator rejects `MsgRotateConsPubKey` (§4.2, ADR-015): rotation makes VEs
  unverifiable for two heights.

### T2 — x/admin (from x/poa)

- `x/poa` becomes `x/admin` (proto `keystone.admin.v1`): `Params{admin}` and an allowlist
  of validator operators. `MsgUpdateParams`, `MsgSetAllowlist{admin, add, remove}`.
- Removing an allowlisted validator jails it if bonded, after the custody removal guard
  approves. Operators, powers, `MsgSetValidators` and validator updates are deleted.
- Ante: `MsgCreateValidator` only for allowlisted operators; `MsgUnjail` rejected for
  validators no longer allowlisted.
- Genesis: admin plus an allowlist covering every gentx validator.

### T3 — Validator-set adapter

`app/valset` implements x/oracle's and x/custody's `ValidatorSet` from staking state:
bonded, unjailed validators with power > 0 sorted by operator address (the x/poa contract); total
power; lookup by operator account. Neither module imports x/staking.

### T4 — Custody set = top N (§9.2, §9.4)

- Params `CustodySetSize` (N), `CustodyEntryMarginBps`, `MinCustodySelfBond`.
- The custody candidates are the bonded validators with custody keys, top N by power, where
  an incumbent of the active epoch is displaced only by an outsider whose power exceeds the
  weakest incumbent's by the margin. Delay, DKG, sweeps and emergency rekey are unchanged.
- Ante guard: a custody member holding a share of a funded epoch cannot undelegate or
  redelegate its self-delegation below `MinCustodySelfBond`.

### T5 — Unauthorized-spend slashing (§9.12)

- VE `BitcoinPayload.spends` (field 6): treasury UTXOs spent by a transaction that is not
  a batch in committed state, attested at depth k.
- A > 2/3-of-power tally slashes every share holder of the UTXO's epoch by
  `TheftSlashFraction` at the epoch's activation height, jails and tombstones them, and
  pauses deposits. Every failure is skipped with an event (PreBlocker totality).

### T6 — Rewards with zero gas (§7.7)

`x/mint` inflation in `ukey`; `StakerShareBps` of each block's stability-fee accrual moves
from the surplus buffer to the fee collector, so `x/distribution` pays KUSD to stakers.
Gas price stays zero; rate limits stay.

### T7 — Genesis and node tooling

`testnet init-files` (accounts with `ukey`, gentxs, allowlist), the genesis ceremony,
`scripts/add-validator.sh` (replaces `add-operator.sh`), signer config wording.

### T8 — Harnesses

`testutil/apptest` and `testutil/multinode` build genesis through gentxs with unequal
power; the determinism workload delegates, undelegates and removes a validator from the
allowlist. `app/pos_integration_test.go` replaces the PoA integration test.

## Acceptance checks

1. `make lint test` passes; the determinism run (1,000 heights, and 10,000 with
   `make determinism-long`) passes with unequal and changing power.
2. VEs verify across validator power changes; `MsgRotateConsPubKey` is rejected; a
   non-allowlisted `MsgCreateValidator` is rejected.
3. Custody: a validator entering the top N triggers `custody_membership_pending`, a DKG
   for the next epoch and a sweep; a power change under the margin does not.
4. An attested unauthorized spend slashes and jails every share holder of the epoch and
   pauses deposits; a slashing failure does not stop the block.
5. Stakers receive `ukey` inflation and a KUSD share of stability fees; gas stays zero.
6. `scripts/demo-M7.sh` (`make e2e`) runs end to end on a 4-node localnet with unequal stake.
