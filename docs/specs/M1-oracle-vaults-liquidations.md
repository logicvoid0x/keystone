# M1 — Single-node slice: oracle, vaults, liquidations

**Goal:** on one node, prices flow from a mock feed through vote extensions into
the oracle; users open vaults and mint KUSD; a price drop liquidates a vault in
the next block through the stability pool. Custody is a stub; collateral is
credited by a dev-only message.

**Design sections:** §5.9, §6 (all), §7 (all), §8 (all), §10, §11, Appendix A.

**Estimate:** 2–4 hours.

## Tasks

### T1 — Price sidecar and mock feed (`sidecar/price`, `cmd/mockfeed`)

- `cmd/mockfeed`: HTTP server for tests and localnet.
  - `GET /ex/{a|b|c}/btcusd` returns `{"price":"100000.00","ts":<unix>}` (three fake exchanges).
  - `POST /admin/price {"usd":"76000"}` sets the base price; `POST /admin/offset {"exchange":"a","bps":30}`
    skews one exchange; `POST /admin/down {"exchange":"a"}` makes it return 503.
- `sidecar/price.Fetcher`: polls each configured source every 1 s, drops quotes older than
  10 s, keeps the median of the rest in a mutex-guarded snapshot `{price_micro_usd, num_sources, at}`.
  Fewer than `MinSources` (3) fresh quotes → no snapshot.
- Real exchange adapters (Coinbase, Kraken, Bitstamp) behind the same `Source` interface;
  configured in `app.toml` under `[keystone.price]`. Localnet and tests use mockfeed URLs.
- `ExtendVote` fills `VoteExtension.oracle` from the snapshot. It never blocks on network I/O.

### T2 — `x/oracle` (design §6)

- State: `PriceState` (§6.1), TWAP ring buffer of `TwapBlocks` entries, miss bit array and
  counters per validator (prefixes in §10.1).
- `AggregatePrice` exactly as the §6.3 code: power-weighted median, reporter quorum
  `reported*3 > total*2`, ±`MaxStepBps` clamp against the previous spot, no clamp at bootstrap.
- Status every block (§6.4): `STALE` after `StaleBlocks` without update; `VOLATILE` when
  |spot − TWAP| × 10,000 > TWAP × `VolatileBps`; else `ACTIVE`.
- Misses (§6.6): no payload, or price more than `OutlierBps` from the median. Over `MissAlert`
  in `MissWindow` → event `oracle_miss_threshold`, window resets. No jailing.
- Keeper: `ApplyAggregate`, `GetPriceState`, `LiquidationPrice` (spot; false if STALE),
  `CollateralPrice` (min(spot, TWAP); false if STALE), `IsActiveForLiquidation`.
- Events `price_updated`, `price_status`, `oracle_miss_threshold`. Queries `Price`, `Twap`, `MissCounter`.

### T3 — `x/stablecoin` (design §7.4)

- KUSD denom `ukusd`. `Mint`, `BurnFromModule`, `AccrueSurplus` (mints to `surplus`),
  `RecordBadDebt`, `CoverShortfall`.
- `sin` stored as `sdkmath.Int`. EndBlocker `Heal`: burn min(surplus, sin) from `surplus`, reduce `sin`.
- Mint pause when `sin` > `SinPauseBps` of supply for `SinPauseBlocks` blocks; event `mints_paused`.
- Queries `Surplus`, `BadDebt`.

### T4 — `x/vault` (design §7, §8.1)

- State per §10.1: vaults, owner index, liquidation index, `RateState`, total art.
- `rpow` in RAY with half-up rounding (MakerDAO `jug` algorithm). `Drip(blockTime)`:
  `rate = rate * rpow(fee, Δt) / RAY` (floor), mint the fee revenue to surplus (§7.3).
- Debt `ceil(art*rate/RAY)`. Mint: `art += ceil(d*RAY/rate)`. Repay: `art -= floor(r*RAY/rate)`,
  or `art = 0` on full repay.
- Liquidation index key exactly §8.1: `(MaxUint256 − art*WAD/collateralSat)` as 32-byte
  big-endian, then vault id as 8-byte big-endian; rewritten on every change to art or collateral;
  vaults with art = 0 are not indexed.
- Messages and checks per §7.2: `MsgOpenVault`, `MsgMint`, `MsgRepay`, `MsgWithdrawCollateral`,
  `MsgCloseVault`, `MsgDepositClaim`. In M1, `MsgWithdrawCollateral` moves satoshis into the
  owner's claimable balance in the custody stub (real Bitcoin withdrawals arrive in M4a).
- `MsgDevCreditCollateral{admin, vault_id, sat}`: compiled only with build tag `devcredit`,
  signer must be the x/poa admin, credits collateral directly. Removed in M3.
- `IterateLiquidatable(price, fn)` walks the index ascending and stops at the first vault
  that fails the exact test in §8.1.

### T5 — `x/liquidation` (design §8)

- Stability pool with Liquity's product/sum scalars (`P`, `S`, `epoch`, `scale`), state per §10.1.
  `MsgDepositPool`, `MsgWithdrawPool` (blocked while any vault is liquidatable at the current price).
- `Select(price)` exactly as §8.2 (cap `MaxPerBlock`, per-vault mode POOL or AUCTION,
  pool balance decremented as entries are chosen).
- `Execute(batch, price)`: pool mode per §8.4 steps 1–4; auction mode per §8.4
  (`MsgTakeCollateral`, price decay `AuctionStepBps` per block, reset at `AuctionResetBps` or
  `AuctionMaxBlocks`). Seized BTC and purchases are credited with `custody.CreditClaimable`.
- Totality: a vault that is no longer liquidatable when executed is skipped with an event.
- Events `vault_liquidated`, `auction_started`, `auction_taken`, `auction_reset`.
- **Flag for audit** in code comments on the P/S math.

### T6 — `x/custody` stub

Only what M1 needs: claimable balances (`address → sat`), `CreditClaimable`, credited-sat
counter, `CapHeadroom` = `TreasuryCapSat − credited`, query `Claimable`. The rest arrives in M3.

### T7 — `protocol.Aggregate` and PreBlocker (design §5.9)

- `Aggregate(votes, total)` builds the oracle part using `AggregatePrice`; bitcoin and frost
  parts return empty in M1.
- PreBlocker order: `oracle.ApplyAggregate` → `vault.Drip` → `liquidation.Select` →
  `liquidation.Execute` → `custody.ApplyOps` (no-op in M1).
- Every step follows the totality rule: skip and emit an event, never return an error for valid tx0.

### T8 — Invariants (design Appendix A)

Implement as functions in `x/<module>/keeper/invariants.go`: 1 supply, 2 total art, 3 rate,
4 dust, 5 ceiling, 7 liquidation index, 8 pool, 9 oracle step, 14 treasury cap.
With build tag `invariants`, an EndBlocker in the app runs all of them and panics on failure.
`make test` builds with `-tags invariants,devcredit`.

### T9 — CLI

autocli tx and query commands for every message and query in this milestone.

## Required test vectors

| Case | Input | Expected |
| --- | --- | --- |
| Per-second fee | 2% per year | `StabilityFeePerSecond = 1000000000627937192491029810` |
| rpow, 30 days | `rpow(fee, 2592000)` | `1001628938483711657286055762` |
| rpow, 1 year | `rpow(fee, 31536000)` | `1019999999999999999972831879` |
| Debt after 30 days | art = 30,000,000,000 ukusd, rate above | debt = `30048868155` ukusd (ceil) |
| Liquidation boundary | collateral 50,000,000 sat, debt above, LR 13000 | liquidatable iff price < `78127057203` micro-USD |
| Seizure | debt above, P = 76,000,000,000, penalty 1000 bps | seize `43491783` sat; owner keeps `6508217` sat |
| Median (§6.3) | powers 1,1,1,1; prices 99,990 / 100,020 / 100,050 / 150,000 | median 100,020; spot 100,020 with previous spot 99,800 |
| Clamp walk | previous 100,000, every report 76,000, MaxStepBps 500 | spots 95,000 → 90,250 → 85,737.5 → 81,450.625 → 77,378.09375 → 76,000 |
| Quorum | 4 operators, 2 report | no update (2×3 ≤ 4×2) |

## Acceptance checks

1. `make lint test` passes (tags `invariants,devcredit`), including all vectors above.
2. A property test runs 5,000 random sequences of open/mint/repay/withdraw/price change and
   checks every implemented invariant after each block.
3. `scripts/demo-M1.sh` on one node with mockfeed at $100,000:
   1. account A opens vault 1; admin dev-credits 50,000,000 sat; A mints 30,000 KUSD;
   2. account B opens vault 2; admin dev-credits 200,000,000 sat; B mints 100,000 KUSD and
      deposits 50,000 KUSD into the stability pool;
   3. set mockfeed to $76,000; the script follows the spot walk above;
   4. vault 1's liquidation price is about $78,000 (1.3 × 30,000 ÷ 0.5, plus a few blocks of
      fees), so it is liquidated in the block where spot first reaches 77,378.09, via the pool:
      `vault_liquidated{id:1, mode:POOL}`;
   5. vault 1 debt is 0; B's claimable BTC equals the seized amount (within rounding);
   6. take mockfeed down for all exchanges; after 20 blocks status is `STALE` and
      `MsgMint` is rejected; bring it back and status returns to `ACTIVE`.
