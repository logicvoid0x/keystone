package btcwatch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/sidecar/btcwatch"
	"github.com/OWNER/keystone/testutil/btcfake"
)

type staticView struct{ v btcwatch.View }

func (s *staticView) Load() (btcwatch.View, error) { return s.v, nil }

func script(b byte) []byte { return append([]byte{0x51, 0x20}, bytes.Repeat([]byte{b}, 32)...) }

func setup(k uint32) (*btcfake.Chain, *staticView, *btcwatch.Watcher) {
	c := btcfake.New(10)
	v := &staticView{btcwatch.View{Network: "regtest", ConfirmDepth: k, Scripts: map[string]bool{string(script(1)): true, string(script(2)): true}, Deposits: map[btcwatch.Outpoint]btcwatch.Record{}}}
	return c, v, btcwatch.New(c, v, btcwatch.Config{Network: "regtest", ScanWindow: 50}, nil)
}

func snap(t *testing.T, w *btcwatch.Watcher) *vetypes.BitcoinPayload {
	t.Helper()
	require.NoError(t, w.Update(context.Background()))
	p, ok := w.Snapshot()
	require.True(t, ok)
	return p
}

func TestDepositNeedsKConfirmations(t *testing.T) {
	c, _, w := setup(3)
	txid := c.Send(script(1), 50_000_000)
	c.Send(append([]byte{0x00, 0x14}, make([]byte, 20)...), 1_000) // not P2TR
	c.Send(script(9), 1_000)                                       // not registered
	c.Mine(2)                                                      // 2 confirmations
	p := snap(t, w)
	require.Empty(t, p.Deposits)
	require.Equal(t, uint64(btcfake.FeeRate), p.FeerateSatVb)
	require.Equal(t, uint32(c.Height()-3), p.Anchor.Height)

	c.Mine(1) // 3 confirmations
	p = snap(t, w)
	require.Len(t, p.Deposits, 1)
	d := p.Deposits[0]
	require.Equal(t, txid[:], d.Txid)
	require.Equal(t, uint64(50_000_000), d.AmountSat)
	require.Equal(t, script(1), d.PkScript)
	require.Equal(t, uint32(c.Height()-2), d.Block.Height)
	h, err := c.BlockHash(context.Background(), c.Height()-2)
	require.NoError(t, err)
	require.Equal(t, h[:], d.Block.Hash)
	hash, err := c.BlockHash(context.Background(), int64(p.Anchor.Height))
	require.NoError(t, err)
	require.Equal(t, hash[:], p.Anchor.Hash)
}

func TestAcceptedDepositsAreNotRepeated(t *testing.T) {
	c, v, w := setup(1)
	txid := c.Send(script(1), 50_000_000)
	c.Mine(1)
	p := snap(t, w)
	require.Len(t, p.Deposits, 1)
	bh, _ := c.BlockHash(context.Background(), c.Height())
	v.v.Deposits[btcwatch.Outpoint{Txid: txid, Vout: 0}] = btcwatch.Record{Height: uint32(c.Height()), Hash: bh}
	p = snap(t, w)
	require.Empty(t, p.Deposits)
	require.Empty(t, p.Reorgs)
}

func TestReorgSeen(t *testing.T) {
	c, v, w := setup(1)
	kept := c.Send(script(1), 10_000_000)
	dropped := c.Send(script(2), 20_000_000)
	c.Mine(2)
	p := snap(t, w)
	require.Len(t, p.Deposits, 2)
	h := c.Height() - 1
	bh, _ := c.BlockHash(context.Background(), h)
	for _, txid := range []chainhash.Hash{kept, dropped} {
		v.v.Deposits[btcwatch.Outpoint{Txid: txid}] = btcwatch.Record{Height: uint32(h), Hash: bh}
	}

	// Invalidate both blocks; one tx is re-mined in a new block, one is gone.
	c.Reorg(2, dropped)
	p = snap(t, w)
	require.Len(t, p.Reorgs, 2, "both deposits' block left the best chain")
	for _, r := range p.Reorgs {
		require.Equal(t, bh[:], r.OldBlockHash)
	}
	require.Empty(t, p.Deposits, "the re-mined one is attested again only once its old credit is reverted")

	// After both reversals, only the re-mined deposit is attested, in its new block.
	v.v.Deposits = map[btcwatch.Outpoint]btcwatch.Record{}
	p = snap(t, w)
	require.Len(t, p.Deposits, 1)
	require.Equal(t, kept[:], p.Deposits[0].Txid)
	require.NotEqual(t, bh[:], p.Deposits[0].Block.Hash)
}

func TestDepositCapOldestFirstCanonical(t *testing.T) {
	c, _, w := setup(1)
	for i := 0; i < 10; i++ {
		c.Send(script(1), int64(1000+i))
	}
	c.Mine(1)
	for i := 0; i < 10; i++ {
		c.Send(script(2), int64(2000+i))
	}
	c.Mine(1)
	p := snap(t, w)
	require.Len(t, p.Deposits, protocol.MaxDeposits)
	older := 0
	for _, d := range p.Deposits {
		if d.AmountSat < 2000 {
			older++
		}
	}
	require.Equal(t, 10, older, "every deposit of the older block is included")
	require.True(t, protocol.IsCanonical(&vetypes.VoteExtension{Bitcoin: p}))
	_, err := (&vetypes.VoteExtension{Version: 1, Height: 1, Bitcoin: p}).Marshal()
	require.NoError(t, err)
}

func TestFailuresClearTheSnapshot(t *testing.T) {
	c, v, w := setup(1)
	snap(t, w)
	c.Fail = errors.New("connection refused")
	require.Error(t, w.Update(context.Background()))
	_, ok := w.Snapshot()
	require.False(t, ok)
	c.Fail = nil
	snap(t, w)

	v.v.Network = "signet"
	require.ErrorContains(t, w.Update(context.Background()), "network")
	_, ok = w.Snapshot()
	require.False(t, ok)
}

// A reorg deeper than the window, and a chain that shrinks, resync cleanly.
func TestDeepReorgResync(t *testing.T) {
	c, _, w := setup(1)
	c.Mine(100)
	snap(t, w)
	c.Reorg(80)
	p := snap(t, w)
	h, _ := c.BlockHash(context.Background(), int64(p.Anchor.Height))
	require.Equal(t, h[:], p.Anchor.Hash)
}

func TestFeeRateConversion(t *testing.T) {
	for in, want := range map[string]uint64{"0.00001000": 1, "0.000015": 2, "0.0002": 20, "0": 0} {
		got, err := btcwatch.BtcPerKvBToSatPerVB(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	_, err := btcwatch.BtcPerKvBToSatPerVB("x")
	require.Error(t, err)
}

func TestRPCClient(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		require.Equal(t, "u", user)
		require.Equal(t, "p", pass)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		seen = append(seen, req.Method)
		switch req.Method {
		case "getblockcount":
			_, _ = rw.Write([]byte(`{"result":123,"error":null,"id":1}`))
		case "estimatesmartfee":
			_, _ = rw.Write([]byte(`{"result":{"feerate":0.00003,"blocks":6},"error":null,"id":1}`))
		default:
			_, _ = rw.Write([]byte(`{"result":null,"error":{"code":-32601,"message":"Method not found"},"id":1}`))
		}
	}))
	defer srv.Close()
	r := btcwatch.NewRPC(srv.URL, "u", "p")
	h, err := r.TipHeight(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(123), h)
	fee, err := r.FeeRate(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(3), fee)
	r.FixedFeeRate = 2
	fee, _ = r.FeeRate(context.Background())
	require.Equal(t, uint64(2), fee)
	_, err = r.Chain(context.Background())
	require.ErrorContains(t, err, "Method not found")
	require.Equal(t, []string{"getblockcount", "estimatesmartfee", "getblockchaininfo"}, seen)
}

// Signed withdrawals are submitted on first sight and every RebroadcastBlocks Keystone
// blocks; once mined at depth k the watcher attests TxConfirmed.
func TestBroadcastAndConfirm(t *testing.T) {
	c, v, w := setup(2)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&chainhash.Hash{7}, 0), nil, nil))
	tx.AddTxOut(wire.NewTxOut(10_000, script(5))) // change to a P2TR output
	var buf bytes.Buffer
	require.NoError(t, tx.Serialize(&buf))
	id := tx.TxHash()
	v.v.Height = 100
	v.v.Signed = []btcwatch.SignedTx{{Txid: id, Raw: buf.Bytes()}}
	v.v.Watched = []chainhash.Hash{id}

	snap(t, w)
	require.True(t, c.InMempool(id))
	require.Equal(t, 1, c.Submissions)
	snap(t, w)
	require.Equal(t, 1, c.Submissions, "not resubmitted within RebroadcastBlocks")
	v.v.Height = 100 + btcwatch.RebroadcastBlocks
	snap(t, w)
	require.Equal(t, 2, c.Submissions, "resubmitted after RebroadcastBlocks")
	require.Len(t, c.Sent, 1)

	c.Mine(1)
	p := snap(t, w)
	require.Empty(t, p.Confirms, "1 confirmation < k = 2")
	c.Mine(1)
	p = snap(t, w)
	require.Len(t, p.Confirms, 1)
	require.Equal(t, id[:], p.Confirms[0].Txid)
	require.Equal(t, uint32(c.Height()-1), p.Confirms[0].Block.Height)
}

// spendTx spends prev with one P2TR output and puts it in the mempool.
func spendTx(t *testing.T, c *btcfake.Chain, prev btcwatch.Outpoint) chainhash.Hash {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&prev.Txid, prev.Vout), nil, nil))
	tx.AddTxOut(wire.NewTxOut(10_000, script(9)))
	var buf bytes.Buffer
	require.NoError(t, tx.Serialize(&buf))
	require.NoError(t, c.SendRawTransaction(context.Background(), buf.Bytes()))
	return tx.TxHash()
}

// Design §9.12 / spec M7-T5: a Keystone UTXO spent at depth k by a transaction that is
// not a batch version is attested as UnauthorizedSpend; batch spends and spends of
// other outputs are not.
func TestUnauthorizedSpend(t *testing.T) {
	c, v, w := setup(2)
	ours := btcwatch.Outpoint{Txid: c.Send(script(1), 50_000_000), Vout: 0}
	batchSpent := btcwatch.Outpoint{Txid: c.Send(script(2), 40_000_000), Vout: 0}
	other := btcwatch.Outpoint{Txid: c.Send(script(9), 1_000), Vout: 0}
	c.Mine(1)
	v.v.Utxos = map[btcwatch.Outpoint]bool{ours: true, batchSpent: true}

	thief := spendTx(t, c, ours)
	batch := spendTx(t, c, batchSpent)
	spendTx(t, c, other)
	v.v.BatchTxids = map[chainhash.Hash]bool{batch: true}

	c.Mine(1)
	require.Empty(t, snap(t, w).Spends, "1 confirmation < k = 2")
	c.Mine(1)
	p := snap(t, w)
	require.Len(t, p.Spends, 1)
	s := p.Spends[0]
	require.Equal(t, ours.Txid[:], s.PrevTxid)
	require.Equal(t, uint32(0), s.PrevVout)
	require.Equal(t, thief[:], s.SpendingTxid)
	require.Equal(t, uint32(c.Height()-1), s.Block.Height)
}
