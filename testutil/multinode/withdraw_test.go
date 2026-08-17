package multinode_test

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/testutil/devdealer"
	"github.com/OWNER/keystone/testutil/multinode"
	custodykeeper "github.com/OWNER/keystone/x/custody/keeper"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

func payout(b byte) []byte { return append([]byte{0x00, 0x14}, bytes.Repeat([]byte{b}, 20)...) }

// withdrawNet is a 4-node network with an ACTIVE price and vault 1 (user 0) funded
// with 5 BTC.
func withdrawNet(t *testing.T) *multinode.Net {
	t.Helper()
	net := multinode.New(t, 4)
	net.SetAllPrices(100_000)
	steps(net, 3)
	u := net.Users[0]
	net.Step(multinode.StepOpts{Txs: [][]byte{net.SignTx(u.Key, &vaulttypes.MsgOpenVault{Owner: u.Addr.String()})}})
	net.Fund(1, 500_000_000)
	return net
}

func withdrawTx(net *multinode.Net, sat int64, to byte) []byte {
	u := net.Users[0]
	return net.SignTx(u.Key, &vaulttypes.MsgWithdrawCollateral{Owner: u.Addr.String(), VaultId: 1, AmountSat: sdkmath.NewInt(sat), RecipientScript: payout(to)})
}

// verifySigned runs every input of a SIGNED batch's current tx through the btcd script
// engine, with the prevouts from the reference node's custody state.
func verifySigned(t *testing.T, net *multinode.Net, b custodytypes.WithdrawalBatch) {
	t.Helper()
	k := net.Ref().App.CustodyKeeper
	ctx := net.Context()
	cur := b.Txs[len(b.Txs)-1]
	tx, err := custodytypes.DeserializeTx(cur.SignedTx)
	require.NoError(t, err)
	e, err := k.Epochs.Get(ctx, b.Epoch)
	require.NoError(t, err)
	p, _, err := custodytypes.ParseGroupKey(e.GroupKey)
	require.NoError(t, err)
	prev := map[wire.OutPoint]*wire.TxOut{}
	for i, op := range b.Inputs {
		txid, vout, ok := custodykeeper.SplitOutpoint(op)
		require.True(t, ok)
		u, err := k.Utxos.Get(ctx, collections.Join(txid, vout))
		require.NoError(t, err)
		root := []byte{}
		if v, err := k.ScriptVault.Get(ctx, net.DepositScript(1)); err == nil && v == 1 {
			root = custodytypes.VaultMessage(multinode.ChainID, b.Epoch, 1)
		}
		q := txscript.ComputeTaprootOutputKey(p, root)
		if _, tw, _ := custodytypes.TreasuryScript(p); bytes.Equal(u.Tweak, tw) {
			q = txscript.ComputeTaprootKeyNoScript(p)
		}
		script, err := txscript.PayToTaprootScript(q)
		require.NoError(t, err)
		prev[tx.TxIn[i].PreviousOutPoint] = wire.NewTxOut(int64(u.AmountSat), script)
	}
	fetcher := txscript.NewMultiPrevOutFetcher(prev)
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for i, in := range tx.TxIn {
		po := prev[in.PreviousOutPoint]
		vm, err := txscript.NewEngine(po.PkScript, tx, i, txscript.StandardVerifyFlags, nil, hashes, po.Value, fetcher)
		require.NoError(t, err)
		require.NoError(t, vm.Execute(), "batch %d input %d", b.Id, i)
	}
}

// TestWithdrawalsSigned is spec M4a-T8: a 4-node run signs 50 withdrawals with zero
// app-hash divergence (net.Step compares every node's app hash each height). Signed
// transactions are broadcast by the watchers to the fake chain, mined, attested and
// confirmed; every signed transaction passes the btcd script engine.
func TestWithdrawalsSigned(t *testing.T) {
	net := withdrawNet(t)
	k := net.Ref().App.CustodyKeeper
	verified := map[uint64]bool{}
	sent := 0
	for h := 0; h < 200; h++ {
		var txs [][]byte
		for j := 0; j < 5 && sent < 50; j++ { // MaxTxPerAccountPerBlock = 5
			txs = append(txs, withdrawTx(net, int64(100_000+1_000*sent), byte(sent)))
			sent++
		}
		r := net.Step(multinode.StepOpts{Txs: txs})
		for _, tr := range r.Finalize[0].TxResults[1:] {
			require.Zero(t, tr.Code, tr.Log)
		}
		ctx := net.Context()
		require.NoError(t, k.Batches.Walk(ctx, nil, func(id uint64, b custodytypes.WithdrawalBatch) (bool, error) {
			if b.Status == custodytypes.BATCH_STATUS_SIGNED && !verified[id] {
				verifySigned(t, net, b)
				verified[id] = true
			}
			return false, nil
		}))
		if h%3 == 2 {
			net.Bitcoin.Mine(1) // mines whatever the watchers broadcast
		}
		completed := 0
		require.NoError(t, k.Requests.Walk(ctx, nil, func(_ uint64, req custodytypes.WithdrawalRequest) (bool, error) {
			if req.Status == custodytypes.REQUEST_STATUS_COMPLETED {
				completed++
			}
			return false, nil
		}))
		if completed == 50 {
			require.NotEmpty(t, verified)
			t.Logf("50 withdrawals completed at height %d in %d batches", net.Height, len(verified))
			// Every batch's change went back to the treasury; invariants held throughout
			// (the invariants build panics in EndBlock otherwise).
			has, err := k.InFlight.Has(ctx, devdealer.Epoch)
			require.NoError(t, err)
			require.False(t, has)
			return
		}
	}
	t.Fatal("50 withdrawals did not complete")
}

// lowestOperator is the node with the lowest operator address: always among the t
// signers chosen for a fresh session (§9.10 sorts by misses, then address).
func lowestOperator(net *multinode.Net) *multinode.Node {
	best := net.Nodes[0]
	for _, nd := range net.Nodes[1:] {
		if bytes.Compare(nd.Addr, best.Addr) < 0 {
			best = nd
		}
	}
	return best
}

// runToSigned steps until batch id is SIGNED (at most n heights).
func runToSigned(t *testing.T, net *multinode.Net, id uint64, n int) custodytypes.WithdrawalBatch {
	t.Helper()
	k := net.Ref().App.CustodyKeeper
	for range n {
		net.Step(multinode.StepOpts{})
		if b, err := k.GetBatch(net.Context(), id); err == nil && b.Status == custodytypes.BATCH_STATUS_SIGNED {
			return b
		}
	}
	t.Fatalf("batch %d not signed after %d heights", id, n)
	return custodytypes.WithdrawalBatch{}
}

func stats(t *testing.T, net *multinode.Net, op sdk.AccAddress) custodytypes.SignerStats {
	t.Helper()
	st, err := net.Ref().App.CustodyKeeper.SignerStats.Get(net.Context(), collections.Join(uint64(devdealer.Epoch), op))
	require.NoError(t, err)
	return st
}

// One node sends invalid shares: it is blamed and the retry signs without it.
func TestInvalidShareBlamedAndRetried(t *testing.T) {
	net := withdrawNet(t)
	bad := lowestOperator(net)
	bad.FrostHook = func(p *vetypes.FrostPayload) {
		for i := range p.Shares {
			z := bytes.Clone(p.Shares[i].Z)
			z[31] ^= 1
			p.Shares[i].Z = z
		}
	}
	net.Step(multinode.StepOpts{Txs: [][]byte{withdrawTx(net, 20_000_000, 1)}})
	b := runToSigned(t, net, 1, 40)
	verifySigned(t, net, b)
	require.Equal(t, uint64(1), stats(t, net, bad.Addr).Invalid)
	s, err := net.Ref().App.CustodyKeeper.Sessions.Get(net.Context(), b.Txs[0].SessionIds[0])
	require.NoError(t, err)
	require.Equal(t, uint32(1), s.Attempt)
	for _, a := range s.Signers {
		require.NotEqual(t, bad.Addr.String(), a.Validator)
	}
}

// With one node's signer stopped (3 of 4 = t), a withdrawal still signs: the stopped
// node misses its session and the retry excludes it.
func TestStoppedSignerStillSigns(t *testing.T) {
	net := withdrawNet(t)
	off := lowestOperator(net)
	off.StopSigner = true
	net.Step(multinode.StepOpts{Txs: [][]byte{withdrawTx(net, 20_000_000, 1)}})
	b := runToSigned(t, net, 1, 40)
	verifySigned(t, net, b)
	st := stats(t, net, off.Addr)
	require.Zero(t, st.Invalid)
	require.Equal(t, uint64(1), st.Missed, "the stopped signer missed its first session")
}
