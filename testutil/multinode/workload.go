package multinode

import (
	"context"
	"math/rand"

	"github.com/btcsuite/btcd/chainhash/v2"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/testutil/btcfake"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
	liqtypes "github.com/OWNER/keystone/x/liquidation/types"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
	stablecointypes "github.com/OWNER/keystone/x/stablecoin/types"
	vaultkeeper "github.com/OWNER/keystone/x/vault/keeper"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

// Workload is the seeded random workload of spec M2-T2: user txs (open, mint, repay,
// pool deposit/withdraw), Bitcoin deposits on the shared fake chain with rare reorgs
// and lagging watchers (M3), per-node price noise (±0.3%), occasional empty
// oracle payloads, price shocks of up to −30%, and scheduled outages in which 2 of 4
// nodes report nothing for long enough that the price goes STALE.
type Workload struct {
	net  *Net
	rng  *rand.Rand
	base uint64 // micro-USD

	// OutageEvery and OutageLen schedule 2-node price outages (0 disables).
	OutageEvery, OutageLen int64

	Stats WorkloadStats
	sent  bool // a deposit was sent this height
}

// WorkloadStats counts what happened.
type WorkloadStats struct {
	Txs, Rejected, Liquidations, StaleBlocks, Shocks int
	Mismatches                                       int
	// Bitcoin: deposits accepted and reverted on chain, fake-chain reorgs.
	Deposits, Reverted, BtcReorgs int
}

// NewWorkload seeds a workload on net.
func NewWorkload(net *Net, seed int64) *Workload {
	return &Workload{net: net, rng: rand.New(rand.NewSource(seed)), base: 100_000_000_000, OutageEvery: 400, OutageLen: 30}
}

// setPrices moves the base price and gives each node its own noisy view.
func (w *Workload) setPrices(h int64) {
	r := w.rng.Float64()
	switch {
	case r < 0.01: // shock: −5% .. −30%
		w.base = w.base * uint64(70+w.rng.Intn(26)) / 100
		w.Stats.Shocks++
	case r < 0.35: // drift ±3%
		w.base = w.base * uint64(97+w.rng.Intn(7)) / 100
	}
	if w.base < 20_000_000_000 { // keep the walk in a sane range
		w.base = 20_000_000_000 + uint64(w.rng.Intn(10_000_000_000))
	}
	if w.base > 200_000_000_000 {
		w.base = 150_000_000_000
	}
	inOutage := w.OutageEvery > 0 && h%w.OutageEvery >= w.OutageEvery-w.OutageLen
	for i, nd := range w.net.Nodes {
		switch {
		case inOutage && i < 2:
			nd.Price.Down()
		case w.rng.Float64() < 0.02: // occasional empty oracle payload
			nd.Price.Down()
		default:
			noise := uint64(9970 + w.rng.Intn(61)) // ±0.3%
			nd.Price.SetMicro(w.base * noise / 10_000)
		}
	}
}

// actors are the workload's signers: the three users and each operator account.
func (w *Workload) actors() []Account {
	out := append([]Account(nil), w.net.Users...)
	for _, nd := range w.net.Nodes {
		out = append(out, Account{nd.AccKey, nd.Addr})
	}
	return out
}

// Txs builds this height's user transactions from the reference node's state.
func (w *Workload) Txs() [][]byte {
	net := w.net
	ctx := net.Context()
	vq := vaultkeeper.NewQuerier(net.Ref().App.VaultKeeper)
	ps, _ := net.Ref().App.OracleKeeper.GetPriceState(ctx)
	spot := ps.SpotMicroUsd
	if spot.IsNil() || !spot.IsPositive() {
		spot = sdkmath.NewIntFromUint64(w.base)
	}
	actors := w.actors()
	var txs [][]byte
	per := map[string]int{}
	send := func(a Account, msg sdk.Msg) {
		if per[a.Addr.String()] >= 3 {
			return
		}
		per[a.Addr.String()]++
		txs = append(txs, net.SignTx(a.Key, msg))
	}
	for i, n := 0, w.rng.Intn(5); i < n; i++ {
		a := actors[w.rng.Intn(len(actors))]
		own, err := vq.VaultsByOwner(ctx, &vaulttypes.QueryVaultsByOwnerRequest{Owner: a.Addr.String()})
		if err != nil {
			continue
		}
		var v *vaulttypes.VaultWithDebt
		if len(own.Vaults) > 0 {
			v = &own.Vaults[w.rng.Intn(len(own.Vaults))]
		}
		switch k := w.rng.Intn(8); {
		case k == 0 || v == nil:
			send(a, &vaulttypes.MsgOpenVault{Owner: a.Addr.String()})
		case k == 1: // a Bitcoin deposit on the shared fake chain, credited ~2 heights later
			net.Bitcoin.Send(net.DepositScript(v.Vault.Id), int64(1_000_000+w.rng.Intn(100_000_000)))
			w.sent = true
		case k <= 3: // mint 20–95% of capacity at MCR
			capacity := v.Vault.CollateralSat.Mul(spot).QuoRaw(150_000_000).Sub(v.Debt)
			amt := capacity.MulRaw(int64(20 + w.rng.Intn(76))).QuoRaw(100)
			if amt.GT(sdkmath.NewInt(100_000_000)) {
				send(a, &vaulttypes.MsgMint{Owner: a.Addr.String(), VaultId: v.Vault.Id, Amount: amt})
			}
		case k == 4:
			if v.Debt.IsPositive() {
				amt := v.Debt.MulRaw(int64(10 + w.rng.Intn(100))).QuoRaw(100)
				send(a, &vaulttypes.MsgRepay{Sender: a.Addr.String(), VaultId: v.Vault.Id, Amount: amt})
			}
		case k <= 6:
			bal := net.Ref().App.BankKeeper.GetBalance(ctx, a.Addr, stablecointypes.Denom).Amount
			if amt := bal.MulRaw(int64(10 + w.rng.Intn(90))).QuoRaw(100); amt.IsPositive() {
				send(a, &liqtypes.MsgDepositPool{Depositor: a.Addr.String(), Amount: amt})
			}
		default:
			send(a, &liqtypes.MsgWithdrawPool{Depositor: a.Addr.String(), Amount: sdkmath.NewInt(int64(w.rng.Intn(20_000)) * 1_000_000)})
		}
	}
	return txs
}

// Step runs one workload height and returns its result.
func (w *Workload) Step() StepResult {
	w.setPrices(w.net.Height + 1)
	txs := w.Txs()
	w.bitcoin()
	r := w.net.Step(StepOpts{Txs: txs})
	w.Stats.Txs += len(txs)
	for _, tr := range r.Finalize[0].TxResults[min(1, len(r.Finalize[0].TxResults)):] {
		if tr.Code != 0 {
			w.Stats.Rejected++
		}
	}
	for _, e := range r.Finalize[0].Events {
		if e.Type == liqtypes.EventTypeVaultLiquidated {
			w.Stats.Liquidations++
		}
		switch e.Type {
		case custodytypes.EventTypeDepositAccepted:
			w.Stats.Deposits++
		case custodytypes.EventTypeDepositReverted:
			w.Stats.Reverted++
		}
	}
	if ps, err := w.net.Ref().App.OracleKeeper.GetPriceState(w.net.Context()); err == nil && ps.Status == oracletypes.PRICE_STATUS_STALE {
		w.Stats.StaleBlocks++
	}
	return r
}

// bitcoin advances the fake Bitcoin chain: it mines the deposits sent this height, and
// rarely reorgs the last 1–3 blocks (sometimes dropping a transaction). Each watcher
// misses its update with 3% probability, so nodes attest from different views.
func (w *Workload) bitcoin() {
	c := w.net.Bitcoin
	if w.sent {
		c.Mine(1)
		w.sent = false
	} else if w.rng.Intn(10) == 0 {
		c.Mine(1)
	}
	if w.rng.Intn(250) == 0 {
		depth := 1 + w.rng.Intn(3)
		var drop []chainhash.Hash
		if w.rng.Intn(2) == 0 {
			if b, err := c.Block(context.Background(), mustHash(c, c.Height())); err == nil && len(b.Transactions) > 1 {
				drop = append(drop, b.Transactions[1].TxHash())
			}
		}
		c.Reorg(depth, drop...)
		w.Stats.BtcReorgs++
	}
	for _, nd := range w.net.Nodes {
		nd.SkipBitcoinUpdate = w.rng.Intn(100) < 3
	}
}

func mustHash(c *btcfake.Chain, h int64) chainhash.Hash {
	hash, err := c.BlockHash(context.Background(), h)
	if err != nil {
		panic(err)
	}
	return hash
}
