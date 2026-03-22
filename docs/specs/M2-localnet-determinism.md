# M2 — 4-operator localnet and determinism

**Goal:** four operators run the chain together; a harness proves app hashes never
diverge over 10,000 blocks even though each node sees different prices; faulty
proposers are rejected; the admin multisig can add an operator.

**Design sections:** §5.4–5.9, §6.7, §8.6, §12, §15, §16 (ABCI metrics).

**Estimate:** 2–4 hours.

## Tasks

### T1 — Docker localnet (`localnet/`)

- `make localnet-start`: runs `keystoned testnet init-files --operators 4`, then
  `docker compose up` with 4 `keystoned` containers and one `mockfeed`.
- Each node points its price sources at mockfeed with a different per-node offset
  (node0 0 bps, node1 +10, node2 −15, node3 +25) so local prices differ.
- Ports: RPC 26657 + 10·i, gRPC 9090 + 10·i, Prometheus 26660 + 10·i.
- `make localnet-stop` tears down and keeps the homes; `make localnet-clean` removes them.

### T2 — In-process multi-node harness (`testutil/multinode`)

Simulates CometBFT for N app instances so determinism tests run fast and reproducibly:

- each node has its own DB, its own ed25519 consensus key, and its own stub sidecars
  (price snapshots set per node by the test);
- per height: choose the proposer round-robin; build the extended commit of H−1 from each
  node's real `ExtendVote` output, signed with that node's key using the sign bytes in
  `docs/verified-apis.md` and sorted by power desc, then address asc;
  call `PrepareProposal` on the proposer, `ProcessProposal` on every node (all must accept),
  `FinalizeBlock` with identical requests on every node, then `Commit`;
- after each height, compare app hashes; on mismatch, dump both states' differing keys
  and fail;
- a seeded random generator produces user txs (open, mint, repay, pool deposit/withdraw,
  dev credit) and per-node price noise (±0.3%), occasional empty oracle payloads, and
  price shocks of up to −30%.

### T3 — Determinism run

`go test ./testutil/multinode -run TestDeterminism10k` drives 4 nodes for 10,000 heights
with the random workload. Expected: zero app-hash mismatches, at least one liquidation,
at least one STALE period, all invariants holding. Runs in CI with a shorter 1,000-height
variant; the 10,000 version runs with `-tags long`.

### T4 — Faulty-proposer tests (harness)

The harness lets a test replace the proposer's output. Each case asserts every honest
node's ProcessProposal result:

| Proposer output | Expected |
| --- | --- |
| No tx0 | Reject |
| tx0 at index 1 | Reject |
| Extra magic-prefixed tx (for example a forged liquidation list) | Reject |
| Votes dropped so the list no longer matches LastCommit | Reject |
| Votes reordered | Reject |
| One VE's signature corrupted, verified power still > 2/3 | Accept; that VE is ignored in aggregation |
| Two of four VEs corrupted (≤ 2/3 verified) | Reject |
| tx0 copied from an earlier height | Reject |

After any rejection, the next round (honest proposer) commits and the chain continues.

### T5 — Withheld and empty VEs (harness)

- 1 of 4 nodes sends no oracle payload: price still updates (3/4 > 2/3).
- 2 of 4 send none: no update; `STALE` after 20 blocks; liquidations pause; recovery when they return.
- A node's ExtendVote times out: its VE is minimal; nothing else changes.

### T6 — Faulty proposer on the real localnet

- Build tag `byzantine` compiles a PrepareProposal wrapper controlled by env `KEYSTONE_BYZ`
  (`drop_tx0`, `extra_magic`, `corrupt_one_ve`, `corrupt_two_ve`). Never in release builds.
- `make localnet-byz MODE=extra_magic` restarts node3 with that mode.
- Expected: the chain keeps producing blocks; `keystone_process_proposal_rejects` rises on
  the honest nodes whenever node3 proposes (except `corrupt_one_ve`, which is accepted).

### T7 — Admin multisig adds an operator

- `scripts/add-operator.sh`: creates a 5th node home, has the admin multisig (3 of 4) sign
  `MsgSetValidators` adding it at power 1, starts the node, and checks it signs blocks
  within 5 heights.
- Also checks that a `MsgSetValidators` signed by only 2 of 4 admin keys is rejected.

### T8 — Metrics

Prometheus metrics from design §16 that exist so far: `extend_vote_ms`, `ve_bytes`,
`process_proposal_ms`, `process_proposal_rejects{reason}`, `preblocker_ms`, `ve_unverified{validator}`,
`oracle_spot`, `oracle_participation_bps`, `oracle_status`, `vault_count`, `debt_total`,
`liquidations_total{mode}`, `pool_balance`.

## Acceptance checks

1. `make lint test` passes; `go test -tags long ./testutil/multinode -run TestDeterminism10k` passes.
2. All T4 and T5 cases pass.
3. `scripts/demo-M2.sh`:
   1. `make localnet-start`; all 4 nodes reach height 30 with identical app hashes (checked via RPC);
   2. repeats the M1 liquidation scenario across the localnet and confirms the
      `vault_liquidated` event at the same height on all 4 nodes;
   3. runs `make localnet-byz MODE=extra_magic` for 60 blocks; chain height keeps increasing
      and rejects are counted;
   4. runs `scripts/add-operator.sh`; the 5th operator signs blocks.
