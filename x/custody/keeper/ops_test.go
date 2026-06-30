package keeper_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/x/custody/keeper"
	"github.com/OWNER/keystone/x/custody/types"
	poatypes "github.com/OWNER/keystone/x/poa/types"
)

// fakeVault stands in for x/vault: CreditCollateral credits within the cap headroom
// and sends the rest to claimable, as the real keeper does.
type fakeVault struct {
	k     *keeper.Keeper
	coll  map[uint64]sdkmath.Int
	owner map[uint64]sdk.AccAddress
	shut  map[uint64]bool
}

func (f *fakeVault) CreditCollateral(ctx context.Context, id uint64, sat sdkmath.Int) (sdkmath.Int, error) {
	h, err := f.k.CapHeadroom(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	c := sdkmath.MinInt(h, sat)
	if err := f.k.AddCredited(ctx, c); err != nil {
		return sdkmath.Int{}, err
	}
	if err := f.k.CreditClaimable(ctx, f.owner[id], sat.Sub(c)); err != nil {
		return sdkmath.Int{}, err
	}
	f.coll[id] = f.coll[id].Add(c)
	return c, nil
}

func (f *fakeVault) DebitCollateral(ctx context.Context, id uint64, sat sdkmath.Int) (sdkmath.Int, error) {
	take := sdkmath.MinInt(sat, f.coll[id])
	f.coll[id] = f.coll[id].Sub(take)
	if err := f.k.SubCredited(ctx, take); err != nil {
		return sdkmath.Int{}, err
	}
	return sat.Sub(take), nil
}

func (f *fakeVault) VaultOwner(_ context.Context, id uint64) (sdk.AccAddress, bool, error) {
	o, ok := f.owner[id]
	if !ok {
		return nil, false, errorsmod.Wrapf(types.ErrNotFound, "vault %d", id)
	}
	return o, !f.shut[id], nil
}

func (f *fakeVault) MinDepositSat(context.Context) (sdkmath.Int, error) { return i(10_000), nil }

type opsFixture struct {
	ctx   sdk.Context
	k     *keeper.Keeper
	vault *fakeVault
	vals  []sdk.ConsAddress
	// scripts[v] is vault v's deposit script.
	scripts map[uint64][]byte
}

func newOps(t *testing.T) *opsFixture {
	t.Helper()
	gs := dealtGenesis(t)
	gs.Params.TreasuryCapSat = i(1_000_000_000) // 10 BTC
	fv := &fakeVals{}
	var vals []sdk.ConsAddress
	for n := byte(1); n <= 4; n++ {
		op := poatypes.Operator{OperatorAddress: sdk.AccAddress(bytes.Repeat([]byte{n}, 20)).String(), ConsensusPubkey: bytes.Repeat([]byte{n}, 32), Power: 1}
		fv.ops = append(fv.ops, op)
		vals = append(vals, op.ConsAddress())
	}
	ctx, k := setupWithVals(t, gs, fv)
	f := &opsFixture{ctx: ctx.WithBlockHeight(10), k: k, vals: vals, scripts: map[uint64][]byte{}}
	f.vault = &fakeVault{k: k, coll: map[uint64]sdkmath.Int{}, owner: map[uint64]sdk.AccAddress{}, shut: map[uint64]bool{}}
	k.SetVaultKeeper(f.vault)
	for id, owner := range map[uint64]sdk.AccAddress{1: alice, 2: bob} {
		_, err := k.RegisterVault(f.ctx, id)
		require.NoError(t, err)
		_, script, _, err := k.DepositAddress(f.ctx, id)
		require.NoError(t, err)
		f.scripts[id] = script
		f.vault.owner[id] = owner
		f.vault.coll[id] = sdkmath.ZeroInt()
	}
	return f
}

func h32(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func (f *opsFixture) deposit(txid byte, vault uint64, sat uint64) vetypes.DepositSeen {
	return vetypes.DepositSeen{Txid: h32(txid), Vout: 0, AmountSat: sat, PkScript: f.scripts[vault], Block: &vetypes.BtcBlockRef{Height: 100, Hash: h32(0xB1)}}
}

// step runs BuildOps (with the given voter indices attesting payload) and ApplyOps
// at the next height.
func (f *opsFixture) step(t *testing.T, payload *vetypes.BitcoinPayload, voters ...int) types.CustodyBatch {
	t.Helper()
	f.ctx = f.ctx.WithBlockHeight(f.ctx.BlockHeight() + 1).WithEventManager(sdk.NewEventManager())
	var votes []types.BitcoinVote
	for _, v := range voters {
		votes = append(votes, types.BitcoinVote{ConsAddr: f.vals[v], Payload: payload})
	}
	b, err := f.k.BuildOps(f.ctx, votes)
	require.NoError(t, err)
	require.NoError(t, f.k.ApplyOps(f.ctx, b))
	return b
}

func evAttr(ctx sdk.Context, typ, key string) (string, bool) {
	for _, e := range ctx.EventManager().Events() {
		if e.Type != typ {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == key {
				return a.Value, true
			}
		}
	}
	return "", false
}

func TestTallyQuorumAcrossHeights(t *testing.T) {
	f := newOps(t)
	d := f.deposit(0xA1, 1, 50_000_000)
	pl := &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{d}}

	// 2 of 4 is not > 2/3; the same voter again adds nothing.
	require.True(t, f.step(t, pl, 0, 1).Empty())
	require.True(t, f.step(t, pl, 0, 1).Empty())
	n := 0
	require.NoError(t, f.k.Tallies.Walk(f.ctx, nil, func([]byte, types.Tally) (bool, error) { n++; return false, nil }))
	require.Equal(t, 1, n)

	// A third distinct voter at a later height accepts it.
	b := f.step(t, pl, 2)
	require.Len(t, b.Credits, 1)
	require.Equal(t, i(50_000_000), f.vault.coll[1])
	v, ok := evAttr(f.ctx, types.EventTypeDepositAccepted, types.AttributeKeySat)
	require.True(t, ok)
	require.Equal(t, "50000000", v)
	n = 0
	require.NoError(t, f.k.Tallies.Walk(f.ctx, nil, func([]byte, types.Tally) (bool, error) { n++; return false, nil }))
	require.Zero(t, n, "accepted tallies are deleted")

	// Late VEs repeating the accepted deposit re-reach quorum and are skipped.
	b = f.step(t, pl, 0, 1, 3)
	require.Len(t, b.Credits, 1)
	reason, ok := evAttr(f.ctx, types.EventTypeOpSkipped, types.AttributeKeyReason)
	require.True(t, ok)
	require.Equal(t, "deposit already accepted", reason)
	require.Equal(t, i(50_000_000), f.vault.coll[1], "credited once")
}

func TestTallyDisagreementAndNonOperators(t *testing.T) {
	f := newOps(t)
	a := f.deposit(0xA1, 1, 50_000_000)
	b := a
	b.AmountSat = 60_000_000
	f.ctx = f.ctx.WithBlockHeight(20)
	votes := []types.BitcoinVote{
		{ConsAddr: f.vals[0], Payload: &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{a}}},
		{ConsAddr: f.vals[1], Payload: &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{a}}},
		{ConsAddr: f.vals[2], Payload: &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{b}}},
		{ConsAddr: f.vals[3], Payload: &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{b}}},
		// A non-operator adds no power.
		{ConsAddr: bytes.Repeat([]byte{0xEE}, 20), Payload: &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{a}}},
	}
	batch, err := f.k.BuildOps(f.ctx, votes)
	require.NoError(t, err)
	require.True(t, batch.Empty(), "a lying minority cannot change the amount")
}

func TestTallyExpiry(t *testing.T) {
	f := newOps(t)
	pl := &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{f.deposit(0xA1, 1, 50_000_000)}}
	f.step(t, pl, 0, 1) // height 11
	f.ctx = f.ctx.WithBlockHeight(11 + types.TallyTTL - 2)
	require.True(t, f.step(t, nil).Empty()) // height 11+TTL-1: still alive
	has, err := f.k.TallyExpiry.Has(f.ctx, collections.Join(int64(11), types.TallyKey(types.ATTESTATION_KIND_DEPOSIT, mustBytes(t, pl.Deposits[0]))))
	require.NoError(t, err)
	require.True(t, has)
	require.True(t, f.step(t, nil).Empty()) // height 11+TTL: expired
	require.True(t, f.step(t, pl, 2).Empty(), "the expired voters no longer count")
	b := f.step(t, pl, 0, 1)
	require.Len(t, b.Credits, 1)
}

func TestAnchorNeverDecreases(t *testing.T) {
	f := newOps(t)
	anchor := func(h uint32, hash byte) *vetypes.BitcoinPayload {
		return &vetypes.BitcoinPayload{Anchor: &vetypes.BtcBlockRef{Height: h, Hash: h32(hash)}}
	}
	f.step(t, anchor(100, 1), 0, 1, 2)
	got, err := f.k.Anchor.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(100), got.Height)

	f.step(t, anchor(99, 2), 0, 1, 2, 3)
	_, skipped := evAttr(f.ctx, types.EventTypeOpSkipped, types.AttributeKeyOp)
	require.True(t, skipped)
	got, _ = f.k.Anchor.Get(f.ctx)
	require.Equal(t, uint32(100), got.Height)

	f.step(t, anchor(100, 3), 0, 1, 2) // same height, new hash (a reorg at depth k)
	got, _ = f.k.Anchor.Get(f.ctx)
	require.Equal(t, h32(3), got.Hash)
	f.step(t, anchor(101, 4), 1, 2, 3)
	got, _ = f.k.Anchor.Get(f.ctx)
	require.Equal(t, uint32(101), got.Height)
}

func TestBuildOpsOrderAndMalformed(t *testing.T) {
	f := newOps(t)
	d1, d2 := f.deposit(0xA2, 1, 20_000_000), f.deposit(0xA1, 2, 30_000_000)
	bad := f.deposit(0xA3, 1, 1)
	bad.PkScript = []byte{0x00, 0x14}
	short := f.deposit(0xA4, 1, 1)
	short.Txid = []byte{1}
	pl := &vetypes.BitcoinPayload{
		Anchor:   &vetypes.BtcBlockRef{Height: 7, Hash: h32(9)},
		Deposits: []vetypes.DepositSeen{d2, d1, bad, short},
		Reorgs:   []vetypes.ReorgSeen{{Txid: h32(0xFF), OldBlockHash: h32(1)}, {Txid: h32(2)}},
	}
	b := f.step(t, pl, 0, 1, 2)
	require.Len(t, b.Anchors, 1)
	require.Len(t, b.Credits, 2)
	require.Equal(t, h32(0xA1), b.Credits[0].Txid, "credits sorted by txid")
	require.Len(t, b.Reverts, 1, "malformed attestations are ignored")
	reason, _ := evAttr(f.ctx, types.EventTypeOpSkipped, types.AttributeKeyReason)
	require.Equal(t, "no accepted deposit at this outpoint", reason)
}

func TestCreditDepositRouting(t *testing.T) {
	f := newOps(t)
	// Below MinDepositSat → fee reserve.
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{f.deposit(0x01, 1, 9_999)}}, 0, 1, 2)
	fr, _ := f.k.GetFeeReserve(f.ctx)
	require.Equal(t, i(9_999), fr)
	require.True(t, f.vault.coll[1].IsZero())

	// Within headroom → collateral; UTXO AVAILABLE with tweak t_v; record kept.
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{f.deposit(0x02, 1, 900_000_000)}}, 0, 1, 2)
	require.Equal(t, i(900_000_000), f.vault.coll[1])
	u, err := f.k.Utxos.Get(f.ctx, collections.Join(h32(0x02), uint32(0)))
	require.NoError(t, err)
	require.Equal(t, types.UTXO_STATUS_AVAILABLE, u.Status)
	require.Len(t, u.Tweak, 32)
	require.Equal(t, uint64(1), u.Epoch)

	// Over the cap: 1 BTC of headroom left, 2 BTC sent → 1 BTC claimable.
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{f.deposit(0x03, 2, 200_000_000)}}, 1, 2, 3)
	require.Equal(t, i(100_000_000), f.vault.coll[2])
	cl, _ := f.k.GetClaimable(f.ctx, bob)
	require.Equal(t, i(100_000_000), cl)
	v, ok := evAttr(f.ctx, types.EventTypeDepositOverCap, types.AttributeKeySat)
	require.True(t, ok)
	require.Equal(t, "100000000", v)
	h, _ := f.k.CapHeadroom(f.ctx)
	require.True(t, h.IsZero())

	// At the cap, everything goes to claimable.
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{f.deposit(0x04, 1, 50_000)}}, 0, 1, 2)
	cl, _ = f.k.GetClaimable(f.ctx, alice)
	require.Equal(t, i(50_000), cl)

	// Closed vault → claimable; unknown script → skipped.
	f.vault.shut[2] = true
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{f.deposit(0x05, 2, 70_000)}}, 0, 1, 2)
	cl, _ = f.k.GetClaimable(f.ctx, bob)
	require.Equal(t, i(100_070_000), cl)
	_, overCap := evAttr(f.ctx, types.EventTypeDepositOverCap, types.AttributeKeySat)
	require.False(t, overCap)
	other := f.deposit(0x06, 1, 70_000)
	other.PkScript = append([]byte{0x51, 0x20}, h32(0x77)...)
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{other}}, 0, 1, 2)
	reason, _ := evAttr(f.ctx, types.EventTypeOpSkipped, types.AttributeKeyReason)
	require.Equal(t, "unknown deposit script", reason)

	sum, err := f.k.UtxoSum(f.ctx)
	require.NoError(t, err)
	require.Equal(t, i(9_999+900_000_000+200_000_000+50_000+70_000), sum)
	cr, _ := f.k.GetCredited(f.ctx)
	require.Equal(t, i(1_000_000_000), cr)
}

func TestRevertDeposit(t *testing.T) {
	f := newOps(t)
	d := f.deposit(0x02, 1, 50_000_000)
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{d}}, 0, 1, 2)
	require.Equal(t, i(50_000_000), f.vault.coll[1])

	// A ReorgSeen naming a different block does not apply.
	f.step(t, &vetypes.BitcoinPayload{Reorgs: []vetypes.ReorgSeen{{Txid: d.Txid, OldBlockHash: h32(0xCC)}}}, 0, 1, 2)
	require.Equal(t, i(50_000_000), f.vault.coll[1])

	// The real one: UTXO removed, collateral debited, no deficit.
	f.step(t, &vetypes.BitcoinPayload{Reorgs: []vetypes.ReorgSeen{{Txid: d.Txid, OldBlockHash: d.Block.Hash}}}, 0, 1, 2)
	require.True(t, f.vault.coll[1].IsZero())
	has, _ := f.k.Utxos.Has(f.ctx, collections.Join(d.Txid, uint32(0)))
	require.False(t, has)
	def, _ := evAttr(f.ctx, types.EventTypeDepositReverted, types.AttributeKeyDeficit)
	require.Equal(t, "0", def)
	require.NoError(t, f.k.CheckNoDeficit(f.ctx))
	cr, _ := f.k.GetCredited(f.ctx)
	require.True(t, cr.IsZero())

	// Re-mined in another block: credited again.
	d2 := d
	d2.Block = &vetypes.BtcBlockRef{Height: 101, Hash: h32(0xB2)}
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{d2}}, 0, 1, 2)
	require.Equal(t, i(50_000_000), f.vault.coll[1])

	// The vault lost 30,000,000 sat (e.g. a withdrawal) before the reorg: deficit.
	_, err := f.vault.DebitCollateral(f.ctx, 1, i(30_000_000))
	require.NoError(t, err)
	f.step(t, &vetypes.BitcoinPayload{Reorgs: []vetypes.ReorgSeen{{Txid: d.Txid, OldBlockHash: d2.Block.Hash}}}, 0, 1, 2)
	require.True(t, f.vault.coll[1].IsZero())
	dd, _ := f.k.GetDeficit(f.ctx)
	require.Equal(t, i(30_000_000), dd)
	def, _ = evAttr(f.ctx, types.EventTypeDepositReverted, types.AttributeKeyDeficit)
	require.Equal(t, "30000000", def)
	require.ErrorIs(t, f.k.CheckNoDeficit(f.ctx), types.ErrDeficit)

	// Claimable and fee-reserve parts are reversed too, short parts go to the deficit.
	small := f.deposit(0x09, 1, 5_000)
	f.step(t, &vetypes.BitcoinPayload{Deposits: []vetypes.DepositSeen{small}}, 0, 1, 2)
	f.step(t, &vetypes.BitcoinPayload{Reorgs: []vetypes.ReorgSeen{{Txid: small.Txid, OldBlockHash: small.Block.Hash}}}, 0, 1, 2)
	fr, _ := f.k.GetFeeReserve(f.ctx)
	require.True(t, fr.IsZero())
	dd, _ = f.k.GetDeficit(f.ctx)
	require.Equal(t, i(30_000_000), dd)
}

func mustBytes(t *testing.T, d vetypes.DepositSeen) []byte {
	t.Helper()
	bz, err := d.Marshal()
	require.NoError(t, err)
	return bz
}
