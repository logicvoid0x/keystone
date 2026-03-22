# M3 — Regtest deposits

**Goal:** a user sends regtest BTC to their vault's Taproot address; after k
confirmations, more than 2/3 of operators attest it in vote extensions and the
collateral is credited. Deep reorgs are reversed. The dev credit message is removed.

**Design sections:** §3.2 (treasury cap), §9.1, §9.6, §9.7, §10.1–10.2 (custody state), §11.

**Estimate:** 3–5 hours.

## Group key for M3

Real DKG arrives in M4b. M3 needs only the group **public** key to derive addresses.
Add `keystoned testnet init-files --dev-dealer`: one process generates a FROST key with
t = floor(2n/3)+1, writes each operator's encrypted share to
`<home>/custody/epoch_1/shares.json` (used in M4a), and puts the group public key and
verifying shares in custody genesis as epoch 1 `ACTIVE`. The binary refuses `--dev-dealer`
unless the chain id ends in `-local-<n>`. Until `crypto/frost` exists (M4a), generate the
key with a simple Shamir split in a test-only helper; M4a replaces it with `crypto/frost` keygen.

## Tasks

### T1 — Localnet Bitcoin

- Add one `bitcoind` per node to docker-compose (`-regtest -txindex=0 -zmqpubrawblock -zmqpubhashblock`),
  peered with each other, plus a miner script (`localnet/mine.sh N`) and a funded faucet wallet.
- Node config `[keystone.bitcoin]`: RPC URL, credentials, ZMQ endpoints, network = regtest.

### T2 — Address derivation (§9.6)

In `x/custody/types/taproot.go` (deterministic; uses `btcec/v2` and `txscript`):

- normalize the group key to even y;
- treasury and change output key `Q0 = P + H_TapTweak(x(P))·G` (BIP-86 style);
- per-vault tweak `m_v = TaggedHash("Keystone/vault", chain_id ‖ epoch ‖ vault_id)`,
  `t_v = H_TapTweak(x(P) ‖ m_v)`, `Q_v = P + t_v·G`; script `OP_1 <x(Q_v)>`;
- `MsgOpenVault` stores the script in the registry (`pk_script → vault_id`, `(vault_id, epoch) → pk_script`)
  and returns the bech32m address. Query `DepositAddress{vault_id}`.

Tests: BIP-341 key-path test vectors for the tweak; address round trip with `btcutil`.

### T3 — Bitcoin watcher sidecar (`sidecar/btcwatch`)

- Follows its bitcoind via RPC and ZMQ; keeps the best chain tip.
- Loads the script registry from committed state (refreshed each block via a read-only ChainView).
- Scans each new block (`getblock` verbosity 2) for outputs paying registered scripts.
- Snapshot for ExtendVote (design §9.7 `BitcoinPayload`): anchor = block at tip − k,
  feerate (`estimatesmartfee`, regtest fixed 2 sat/vB), up to 16 `DepositSeen` at depth ≥ k
  not yet accepted on-chain, `ReorgSeen` for credited deposits whose block left the best chain.
- Canonical sort orders from design §9.7.

### T4 — Attestation tallies (`x/custody`)

- `Aggregate` bitcoin part: for each attestation, key = SHA-256(canonical bytes); add each
  distinct validator's power once (bitmap); accept when `power*3 > total*2`; expire after
  600 blocks. Tallies span heights.
- Anchor tally the same way; the accepted anchor height never decreases.
- `BuildOps` emits `AcceptAnchor`, `CreditDeposit`, `RevertDeposit` in that order; `ApplyOps` applies them.

### T5 — Crediting and the treasury cap

- Below `MinDepositSat` → fee reserve. Within `CapHeadroom` → `vault.CreditCollateral` and a
  UTXO `AVAILABLE`. Over the cap → owner's claimable balance (withdraw-only), event `deposit_over_cap`.
- Credited sats counter maintained for the cap and for invariant 6.

### T6 — Reorg path

On an accepted `ReorgSeen`: remove the UTXO, `vault.DebitCollateral`; if the vault holds less
than the amount, collateral goes to 0 and `custody_deficit_sat` records the rest. Event
`deposit_reverted`. While the deficit is non-zero, new withdrawal requests are rejected.

### T7 — Remove the dev credit message

Delete `MsgDevCreditCollateral` and the `devcredit` build tag; update tests and demos to
fund vaults through regtest deposits.

### T8 — Invariant 6

Custody coverage (Appendix A, invariant 6) with the M3 terms: UTXO sum + deficit ≥
credited collateral + claimable + fee reserve.

## Acceptance checks

1. `make lint test` passes; the M2 determinism run still passes with a stub watcher that
   feeds attestations.
2. `scripts/demo-M3.sh` on the localnet:
   1. open a vault, get its address, send 0.5 BTC from the faucet, mine 6 blocks;
   2. within 5 Keystone blocks, `collateral_credited{vault, 50000000}` on all nodes;
   3. mint 30,000 KUSD against it;
   4. send a deposit that would exceed the 10 BTC cap; it lands in claimable, not collateral;
   5. reorg test: send a deposit, mine 6, wait for credit, then `invalidateblock` on every
      bitcoind back past it and mine a longer chain without it; the deposit is reverted.
