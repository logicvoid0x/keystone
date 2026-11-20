package keeper_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
	"github.com/logicvoid0x/keystone/testutil/devdealer"
	"github.com/logicvoid0x/keystone/x/custody/keeper"
	"github.com/logicvoid0x/keystone/x/custody/types"
	"github.com/logicvoid0x/keystone/x/validators"
)

// simSigner is a minimal in-test FROST signer (the real one is sidecar/signer): it keeps
// its nonces in memory, tops its pool up by 2 per block and signs every OPEN session it
// is assigned to.
type simSigner struct {
	op     sdk.AccAddress
	cons   sdk.ConsAddress
	index  uint32
	share  frost.Scalar
	nonces map[uint64]secret.Nonces
	// bad sends z+1 instead of z; silent sends no shares; absent sends no VE.
	bad, silent, absent bool
}

func (s *simSigner) payload(t *testing.T, ctx sdk.Context, k *keeper.Keeper) *vetypes.FrostPayload {
	t.Helper()
	p := &vetypes.FrostPayload{}
	last, pool, err := k.NoncePool(ctx, devdealer.Epoch, s.index)
	require.NoError(t, err)
	for j := 0; j < min(2, 8-len(pool)); j++ {
		seq := last + 1 + uint64(j)
		n, ok := s.nonces[seq]
		if !ok {
			n, err = secret.NewNonces(frost.Taproot, nil, s.share)
			require.NoError(t, err)
			s.nonces[seq] = n
		}
		d, e, err := n.Commitment()
		require.NoError(t, err)
		p.Commitments = append(p.Commitments, vetypes.NonceCommitment{Epoch: devdealer.Epoch, ShareIndex: s.index, Seq: seq, Hiding: d[:], Binding: e[:]})
	}
	if s.silent {
		return p
	}
	sessions, err := k.GetActiveSessions(ctx)
	require.NoError(t, err)
	e, err := k.Epochs.Get(ctx, devdealer.Epoch)
	require.NoError(t, err)
	for _, sess := range sessions {
		if sess.State != types.SESSION_STATE_OPEN {
			continue
		}
		key, err := frost.NewKey(frost.Taproot, e.GroupKey, sess.Tweak)
		require.NoError(t, err)
		var cs []frost.Commitment
		var mine *types.SignerAssignment
		for i, a := range sess.Signers {
			c, err := frost.ParseCommitment(a.ShareIndex, a.Hiding, a.Binding)
			require.NoError(t, err)
			cs = append(cs, c)
			if a.Validator == s.op.String() {
				mine = &sess.Signers[i]
			}
		}
		if mine == nil {
			continue
		}
		sg, err := frost.NewSigning(key, sess.Msg, cs)
		require.NoError(t, err)
		z, err := secret.Sign(sg, mine.ShareIndex, s.share, s.nonces[mine.NonceSeq])
		require.NoError(t, err)
		if s.bad {
			one := frost.Scalar{}
			one.SetInt(1)
			z.Add(&one)
		}
		zb := z.Bytes()
		p.Shares = append(p.Shares, vetypes.CombinedShare{SessionId: sess.Id, Z: zb[:]})
	}
	ve := vetypes.VoteExtension{Frost: p}
	sortFrost(&ve)
	return p
}

func sortFrost(ve *vetypes.VoteExtension) {
	f := ve.Frost
	for i := 1; i < len(f.Shares); i++ {
		for j := i; j > 0 && bytes.Compare(f.Shares[j-1].SessionId, f.Shares[j].SessionId) > 0; j-- {
			f.Shares[j-1], f.Shares[j] = f.Shares[j], f.Shares[j-1]
		}
	}
}

type signFixture struct {
	*opsFixture
	fv *fakeVals
	// hook, if set, runs before each block and returns extra messages to deliver.
	hook    func() []sdk.Msg
	signers []*simSigner
	bitcoin *vetypes.BitcoinPayload // attested by every voter each step
}

// newSigning is a 4-operator custody with a dealt epoch whose operators match the
// validator set, BatchMaxWait 2 and simulated signers.
func newSigning(t *testing.T) *signFixture {
	t.Helper()
	fv := &fakeVals{}
	var ops []sdk.AccAddress
	for n := byte(1); n <= 4; n++ {
		op := validators.Validator{OperatorAddress: sdk.AccAddress(bytes.Repeat([]byte{n}, 20)).String(), ConsensusPubkey: bytes.Repeat([]byte{n}, 32), Power: 1}
		fv.ops = append(fv.ops, op)
		ops = append(ops, sdk.MustAccAddressFromBech32(op.OperatorAddress))
	}
	d, err := devdealer.Deal(rand.New(rand.NewSource(42)), ops)
	require.NoError(t, err)
	gs := types.DefaultGenesis()
	gs.Params.BitcoinNetwork = types.NetworkRegtest
	gs.Params.BatchMaxWait = 2
	d.Apply(gs)
	ctx, k := setupWithVals(t, gs, fv)
	of := &opsFixture{ctx: ctx.WithBlockHeight(10), k: k, scripts: map[uint64][]byte{}}
	for _, op := range fv.ops {
		of.vals = append(of.vals, op.ConsAddress())
	}
	of.vault = &fakeVault{k: k, coll: map[uint64]sdkmath.Int{}, owner: map[uint64]sdk.AccAddress{}, shut: map[uint64]bool{}}
	k.SetVaultKeeper(of.vault)
	for id, owner := range map[uint64]sdk.AccAddress{1: alice, 2: bob} {
		_, err := k.RegisterVault(of.ctx, id)
		require.NoError(t, err)
		_, script, _, err := k.DepositAddress(of.ctx, id)
		require.NoError(t, err)
		of.scripts[id] = script
		of.vault.owner[id] = owner
		of.vault.coll[id] = sdkmath.ZeroInt()
	}
	f := &signFixture{opsFixture: of, fv: fv, bitcoin: &vetypes.BitcoinPayload{FeerateSatVb: 2}}
	for i, op := range d.Operators {
		s, err := frost.ParseScalar(d.Shares[i].Shares[0].Secret)
		require.NoError(t, err)
		f.signers = append(f.signers, &simSigner{op: op, cons: of.vals[i], index: d.Shares[i].Shares[0].Index, share: s, nonces: map[uint64]secret.Nonces{}})
	}
	return f
}

// block runs one height with every signer voting (Bitcoin payload f.bitcoin plus its
// FROST payload computed from the state before the block).
func (f *signFixture) block(t *testing.T) types.CustodyBatch {
	t.Helper()
	var votes []types.Vote
	for _, s := range f.signers {
		if s.absent {
			continue
		}
		votes = append(votes, types.Vote{ConsAddr: s.cons, Bitcoin: f.bitcoin, Frost: s.payload(t, f.ctx, f.k)})
	}
	var msgs []sdk.Msg
	if f.hook != nil {
		msgs = f.hook()
	}
	f.ctx = f.ctx.WithBlockHeight(f.ctx.BlockHeight() + 1).WithEventManager(sdk.NewEventManager())
	b, err := f.k.BuildOps(f.ctx, votes)
	require.NoError(t, err)
	require.NoError(t, f.k.ApplyOps(f.ctx, b))
	deliver(t, f.ctx, f.k, msgs)
	require.NoError(t, f.k.EndBlock(f.ctx))
	for _, e := range f.ctx.EventManager().Events() {
		if e.Type == types.EventTypeOpSkipped {
			for _, a := range e.Attributes {
				if a.Key == types.AttributeKeyReason && a.Value != "no batch due" && a.Value != "anchor unchanged" {
					t.Logf("height %d: %v", f.ctx.BlockHeight(), e)
				}
			}
		}
	}
	require.NoError(t, f.k.CoverageInvariant(f.ctx, sdkmath.ZeroInt()), "height %d", f.ctx.BlockHeight())
	return b
}

// fund credits deposits of sat to vault 1 (all four voters attest).
func (f *signFixture) fund(t *testing.T, txid byte, sats ...uint64) {
	t.Helper()
	var ds []vetypes.DepositSeen
	for vout, sat := range sats {
		d := f.deposit(txid, 1, sat)
		d.Vout = uint32(vout)
		ds = append(ds, d)
	}
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Deposits: ds}
	f.block(t)
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2}
}

func wpkh(b byte) []byte { return append([]byte{0x00, 0x14}, bytes.Repeat([]byte{b}, 20)...) }

func (f *signFixture) queueClaim(t *testing.T, owner sdk.AccAddress, script []byte, sat int64) uint64 {
	t.Helper()
	// Move the satoshis out of vault 1 first, so the books balance (invariant 6).
	short, err := f.vault.DebitCollateral(f.ctx, 1, i(sat))
	require.NoError(t, err)
	require.True(t, short.IsZero())
	require.NoError(t, f.k.CreditClaimable(f.ctx, owner, i(sat)))
	id, err := f.k.WithdrawClaim(f.ctx, &types.MsgWithdrawClaim{Owner: owner.String(), RecipientScript: script, AmountSat: i(sat)})
	require.NoError(t, err)
	return id
}

// runUntil steps blocks until the batch reaches status or n blocks pass.
func (f *signFixture) runUntil(t *testing.T, id uint64, status types.BatchStatus, n int) types.WithdrawalBatch {
	t.Helper()
	for range n {
		f.block(t)
		if b, err := f.k.GetBatch(f.ctx, id); err == nil && b.Status == status {
			return b
		}
	}
	b, err := f.k.GetBatch(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, status, b.Status, "batch %d after %d blocks", id, n)
	return b
}

// verifyTx runs every input of the signed tx through the btcd script engine.
func verifyTx(t *testing.T, f *signFixture, b types.WithdrawalBatch) *wire.MsgTx {
	t.Helper()
	cur := b.Txs[len(b.Txs)-1]
	tx, err := types.DeserializeTx(cur.SignedTx)
	require.NoError(t, err)
	prev := map[wire.OutPoint]*wire.TxOut{}
	for i, op := range b.Inputs {
		txid, vout, ok := keeper.SplitOutpoint(op)
		require.True(t, ok)
		u, err := f.k.Utxos.Get(f.ctx, collectionsJoin(txid, vout))
		require.NoError(t, err)
		script := f.scripts[1]
		if bytes.Equal(u.Tweak, treasuryTweak(t, f)) {
			script = treasuryScript(t, f)
		}
		prev[tx.TxIn[i].PreviousOutPoint] = wire.NewTxOut(int64(u.AmountSat), script)
	}
	fetcher := txscript.NewMultiPrevOutFetcher(prev)
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for i, in := range tx.TxIn {
		po := prev[in.PreviousOutPoint]
		vm, err := txscript.NewEngine(po.PkScript, tx, i, txscript.StandardVerifyFlags, nil, hashes, po.Value, fetcher)
		require.NoError(t, err)
		require.NoError(t, vm.Execute(), "input %d", i)
	}
	return tx
}

func TestWithdrawalSignedAndConfirmed(t *testing.T) {
	f := newSigning(t)
	f.fund(t, 0x11, 30_000_000, 50_000_000)
	carol := sdk.AccAddress(bytes.Repeat([]byte{0xCC}, 20))
	r1 := f.queueClaim(t, alice, wpkh(0x21), 20_000_000)
	r2 := f.queueClaim(t, carol, append([]byte{0x51, 0x20}, bytes.Repeat([]byte{0x22}, 32)...), 1_000_000)

	// The batch opens once the oldest request is BatchMaxWait (2) blocks old, after
	// the signers' pools are filled (2 per block).
	b := f.runUntil(t, 1, types.BATCH_STATUS_SIGNED, 12)
	require.Equal(t, []uint64{r1, r2}, b.RequestIds)
	require.Len(t, b.Inputs, 1, "the 0.5 BTC UTXO covers both requests")
	cur := b.Txs[0]
	tx := verifyTx(t, f, b)
	require.Equal(t, int32(2), tx.Version)
	require.Equal(t, uint32(0), tx.LockTime)
	require.Equal(t, uint32(0xFFFFFFFD), tx.TxIn[0].Sequence)
	require.Len(t, tx.TxOut, 3)

	// Fee accounting: vsize = 10.5 + 57.5 + 43·3 = 197 vB at 2 sat/vB → fee 394; each
	// request pays ceil(2·2·197/2) = 394.
	share := types.FeeShare(2, 1, 2)
	require.Equal(t, uint64(394), share)
	require.Equal(t, uint64(394), cur.FeeSat)
	require.Equal(t, int64(20_000_000-394), tx.TxOut[0].Value)
	require.Equal(t, int64(1_000_000-394), tx.TxOut[1].Value)
	require.Equal(t, int64(50_000_000-21_000_000+2*394-394), tx.TxOut[2].Value)
	fee, err := f.k.GetFeeReserve(f.ctx)
	require.NoError(t, err)
	require.Equal(t, i(2*394-394), fee)
	req, err := f.k.Requests.Get(f.ctx, r1)
	require.NoError(t, err)
	require.Equal(t, types.REQUEST_STATUS_BATCHED, req.Status)
	require.Equal(t, share, req.FeeSat)

	// Confirmation by > 2/3: input spent, change AVAILABLE, requests COMPLETED.
	f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Confirms: []vetypes.TxConfirmed{{Txid: cur.Txid, Block: &vetypes.BtcBlockRef{Height: 120, Hash: h32(0xC1)}}}}
	f.block(t)
	b, err = f.k.GetBatch(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, types.BATCH_STATUS_CONFIRMED, b.Status)
	req, _ = f.k.Requests.Get(f.ctx, r2)
	require.Equal(t, types.REQUEST_STATUS_COMPLETED, req.Status)
	_, err = f.k.Utxos.Get(f.ctx, collectionsJoin(h32(0x11), 1))
	require.Error(t, err, "the spent input is gone")
	change, err := f.k.Utxos.Get(f.ctx, collectionsJoin(cur.Txid, 2))
	require.NoError(t, err)
	require.Equal(t, uint64(tx.TxOut[2].Value), change.AmountSat)
	require.Equal(t, types.UTXO_STATUS_AVAILABLE, change.Status)
	require.Equal(t, treasuryTweak(t, f), change.Tweak)
	has, err := f.k.InFlight.Has(f.ctx, 1)
	require.NoError(t, err)
	require.False(t, has)
	sum, _ := f.k.UtxoSum(f.ctx)
	require.Equal(t, i(30_000_000+int64(change.AmountSat)), sum)
}

// The same state yields the same unsigned transaction, byte for byte (golden digest).
func TestUnsignedTxGolden(t *testing.T) {
	digest := func() string {
		f := newSigning(t)
		f.fund(t, 0x11, 30_000_000, 50_000_000, 7_000_000)
		f.queueClaim(t, alice, wpkh(0x21), 60_000_000)
		f.queueClaim(t, bob, wpkh(0x22), 10_000)
		for range 12 {
			f.block(t)
			if _, err := f.k.GetBatch(f.ctx, 1); err == nil {
				break
			}
		}
		b, err := f.k.GetBatch(f.ctx, 1)
		require.NoError(t, err)
		require.Len(t, b.Inputs, 2, "0.5 + 0.3 BTC, largest first")
		sum := sha256.Sum256(b.Txs[0].UnsignedTx)
		return hex.EncodeToString(sum[:])
	}
	a, b := digest(), digest()
	require.Equal(t, a, b)
	require.Equal(t, goldenUnsignedTx, a)
}

const goldenUnsignedTx = "14090a9edcf27b24407725dbe651a395b407d96a99d5984603fc609c6d25f8d8"

// One operator sends an invalid share: it is blamed for the epoch and the retry signs
// without it.
func TestInvalidShareBlamed(t *testing.T) {
	f := newSigning(t)
	f.fund(t, 0x11, 50_000_000)
	f.queueClaim(t, alice, wpkh(0x21), 20_000_000)
	bad := f.signers[0]
	bad.bad = true
	b := f.runUntil(t, 1, types.BATCH_STATUS_SIGNED, 15)
	verifyTx(t, f, b)
	st, err := f.k.SignerStats.Get(f.ctx, collectionsJoin2(devdealer.Epoch, bad.op))
	require.NoError(t, err)
	require.Equal(t, uint64(1), st.Invalid)
	s, err := f.k.Sessions.Get(f.ctx, b.Txs[0].SessionIds[0])
	require.NoError(t, err)
	require.Equal(t, uint32(1), s.Attempt)
	for _, a := range s.Signers {
		require.NotEqual(t, bad.op.String(), a.Validator, "a blamed operator is not assigned again")
	}
}

// One assigned operator stays silent: at the deadline the session fails with it
// missing, and the retry (which excludes it) signs.
func TestMissingShareRetried(t *testing.T) {
	f := newSigning(t)
	f.fund(t, 0x11, 50_000_000)
	f.queueClaim(t, alice, wpkh(0x21), 20_000_000)
	quiet := f.signers[1]
	quiet.silent = true
	b := f.runUntil(t, 1, types.BATCH_STATUS_SIGNED, 20)
	verifyTx(t, f, b)
	st, err := f.k.SignerStats.Get(f.ctx, collectionsJoin2(devdealer.Epoch, quiet.op))
	require.NoError(t, err)
	require.Equal(t, uint64(1), st.Missed)
	require.Zero(t, st.Invalid)
}

// With two of four operators sending no VE there are too few eligible signers: the
// session stalls until they return.
func TestStalledUntilSignersReturn(t *testing.T) {
	f := newSigning(t)
	f.fund(t, 0x11, 50_000_000)
	f.queueClaim(t, alice, wpkh(0x21), 20_000_000)
	for _, s := range f.signers[:2] {
		s.absent = true
	}
	for range 8 {
		f.block(t)
	}
	ss, err := f.k.GetActiveSessions(f.ctx)
	require.NoError(t, err)
	require.Len(t, ss, 1)
	require.Equal(t, types.SESSION_STATE_STALLED, ss[0].State)
	for _, s := range f.signers {
		s.absent = false
	}
	b := f.runUntil(t, 1, types.BATCH_STATUS_SIGNED, 10)
	verifyTx(t, f, b)
}

func TestWithdrawalValidation(t *testing.T) {
	f := newSigning(t)
	require.NoError(t, f.k.CreditClaimable(f.ctx, alice, i(100_000)))
	ms := keeper.NewMsgServerImpl(f.k)
	for name, tc := range map[string]struct {
		script []byte
		sat    int64
		err    error
	}{
		"p2pkh":        {append([]byte{0x76, 0xa9, 0x14}, bytes.Repeat([]byte{1}, 22)...), 20_000, types.ErrInvalidScript},
		"below min":    {wpkh(1), 9_999, types.ErrInvalidAmount},
		"insufficient": {wpkh(1), 100_001, types.ErrInsufficient},
	} {
		_, err := ms.WithdrawClaim(f.ctx, &types.MsgWithdrawClaim{Owner: alice.String(), RecipientScript: tc.script, AmountSat: i(tc.sat)})
		require.ErrorIs(t, err, tc.err, name)
	}
	res, err := ms.WithdrawClaim(f.ctx, &types.MsgWithdrawClaim{Owner: alice.String(), RecipientScript: wpkh(1), AmountSat: i(100_000)})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.RequestId)
	cl, _ := f.k.GetClaimable(f.ctx, alice)
	require.True(t, cl.IsZero())
	require.NoError(t, f.k.Deficit.Set(f.ctx, i(1)))
	require.NoError(t, f.k.CreditClaimable(f.ctx, alice, i(100_000)))
	_, err = ms.WithdrawClaim(f.ctx, &types.MsgWithdrawClaim{Owner: alice.String(), RecipientScript: wpkh(1), AmountSat: i(100_000)})
	require.ErrorIs(t, err, types.ErrDeficit)
}

func collectionsJoin(txid []byte, vout uint32) collections.Pair[[]byte, uint32] {
	return collections.Join(txid, vout)
}

func collectionsJoin2(epoch uint64, op sdk.AccAddress) collections.Pair[uint64, sdk.AccAddress] {
	return collections.Join(epoch, op)
}

func treasuryKey(t *testing.T, f *signFixture) *btcec.PublicKey {
	t.Helper()
	e, err := f.k.Epochs.Get(f.ctx, devdealer.Epoch)
	require.NoError(t, err)
	p, _, err := types.ParseGroupKey(e.GroupKey)
	require.NoError(t, err)
	return p
}

func treasuryTweak(t *testing.T, f *signFixture) []byte {
	t.Helper()
	_, tw, err := types.TreasuryScript(treasuryKey(t, f))
	require.NoError(t, err)
	return tw
}

func treasuryScript(t *testing.T, f *signFixture) []byte {
	t.Helper()
	s, _, err := types.TreasuryScript(treasuryKey(t, f))
	require.NoError(t, err)
	return s
}

// Design §9.10 / §12: signer_flagged is emitted once an operator passes 3 invalid
// shares (here: blamed sessions over 4 batches) or misses more than 20% of its
// assigned sessions.
func TestSignerFlagged(t *testing.T) {
	f := newSigning(t)
	f.fund(t, 0x11, 50_000_000)
	quiet := f.signers[0]
	quiet.silent = true
	flagged := false
	for n := 0; n < 3 && !flagged; n++ {
		f.queueClaim(t, alice, wpkh(0x21+byte(n)), 1_000_000)
		for range 30 {
			f.block(t)
			if op, ok := evAttr(f.ctx, types.EventTypeSignerFlagged, types.AttributeKeyOperator); ok {
				require.Equal(t, quiet.op.String(), op)
				flagged = true
				break
			}
			if b, err := f.k.GetBatch(f.ctx, uint64(n+1)); err == nil && b.Status == types.BATCH_STATUS_SIGNED {
				f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2, Confirms: []vetypes.TxConfirmed{{Txid: b.Txs[0].Txid, Block: &vetypes.BtcBlockRef{Height: uint32(200 + n), Hash: h32(byte(0xD0 + n))}}}}
				f.block(t)
				f.bitcoin = &vetypes.BitcoinPayload{FeerateSatVb: 2}
				break
			}
		}
	}
	require.True(t, flagged, "a signer missing every assigned session is flagged")
	st, err := f.k.SignerStats.Get(f.ctx, collectionsJoin2(1, quiet.op))
	require.NoError(t, err)
	require.True(t, st.Flagged)
	require.Greater(t, st.Missed*5, st.Assigned)
}
