// Package btcfake is an in-memory Bitcoin chain implementing btcwatch.Source, for
// tests and the multi-node determinism harness (spec M3 acceptance 1: "a stub watcher
// that feeds attestations"). Blocks are real wire blocks with linked headers, so the
// watcher runs unchanged against it. Safe for concurrent use.
package btcfake

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"slices"
	"sync"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

// FeeRate is the fixed fee rate the fake reports (sat/vB), as regtest localnets do.
const FeeRate = 2

// Chain is a single best chain plus a mempool.
type Chain struct {
	mu      sync.Mutex
	blocks  []*wire.MsgBlock // blocks[h] is height h; blocks[0] is genesis
	mempool []*wire.MsgTx
	nonce   uint32
	// Sent lists every txid accepted by SendRawTransaction, in order; Submissions
	// counts every call (resubmissions included).
	Sent        []chainhash.Hash
	Submissions int
	// Fail, if set, is returned by every call (bitcoind unreachable).
	Fail error
}

// New returns a chain with a genesis block and `premine` empty blocks after it.
func New(premine int) *Chain {
	c := &Chain{}
	c.blocks = append(c.blocks, c.newBlock(chainhash.Hash{}, 0, nil))
	c.Mine(premine)
	return c
}

func (c *Chain) newBlock(prev chainhash.Hash, height int64, txs []*wire.MsgTx) *wire.MsgBlock {
	c.nonce++
	coinbase := wire.NewMsgTx(2)
	var hb [12]byte
	binary.BigEndian.PutUint64(hb[:8], uint64(height))
	binary.BigEndian.PutUint32(hb[8:], c.nonce)
	coinbase.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&chainhash.Hash{}, wire.MaxPrevOutIndex), hb[:], nil))
	coinbase.AddTxOut(wire.NewTxOut(50_0000_0000, []byte{0x51}))
	all := append([]*wire.MsgTx{coinbase}, txs...)
	ids := make([]byte, 0, 32*len(all))
	for _, tx := range all {
		h := tx.TxHash()
		ids = append(ids, h[:]...)
	}
	root := chainhash.HashH(ids)
	b := wire.NewMsgBlock(wire.NewBlockHeader(1, &prev, &root, 0x207fffff, c.nonce))
	for _, tx := range all {
		if err := b.AddTransaction(tx); err != nil {
			panic(err)
		}
	}
	return b
}

// Send puts a transaction paying amount to script in the mempool and returns its
// txid (internal byte order).
func (c *Chain) Send(script []byte, amount int64) chainhash.Hash {
	return c.SendMany([]wire.TxOut{{Value: amount, PkScript: script}})
}

// SendMany puts one transaction with the given outputs in the mempool.
func (c *Chain) SendMany(outs []wire.TxOut) chainhash.Hash {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nonce++
	tx := wire.NewMsgTx(2)
	var prev chainhash.Hash
	binary.BigEndian.PutUint32(prev[:4], c.nonce)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&prev, 0), nil, nil))
	for _, o := range outs {
		tx.AddTxOut(wire.NewTxOut(o.Value, slices.Clone(o.PkScript)))
	}
	c.mempool = append(c.mempool, tx)
	return tx.TxHash()
}

// Mine adds n blocks; the first takes the whole mempool.
func (c *Chain) Mine(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < n; i++ {
		tip := c.blocks[len(c.blocks)-1]
		c.blocks = append(c.blocks, c.newBlock(tip.BlockHash(), int64(len(c.blocks)), c.mempool))
		c.mempool = nil
	}
}

// Reorg replaces the last `depth` blocks with depth+1 new ones. Their transactions go
// back to the mempool except those in drop, and the first new block mines the
// mempool (so un-dropped transactions confirm again, in a different block).
func (c *Chain) Reorg(depth int, drop ...chainhash.Hash) {
	c.mu.Lock()
	if depth >= len(c.blocks) {
		depth = len(c.blocks) - 1
	}
	old := c.blocks[len(c.blocks)-depth:]
	c.blocks = c.blocks[:len(c.blocks)-depth]
	var back []*wire.MsgTx
	for _, b := range old {
		for _, tx := range b.Transactions[1:] {
			if !slices.Contains(drop, tx.TxHash()) {
				back = append(back, tx)
			}
		}
	}
	c.mempool = append(back, c.mempool...)
	c.mu.Unlock()
	c.Mine(depth + 1)
}

// Height is the tip height.
func (c *Chain) Height() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.blocks) - 1)
}

// TipHeight implements btcwatch.Source.
func (c *Chain) TipHeight(context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Fail != nil {
		return 0, c.Fail
	}
	return int64(len(c.blocks) - 1), nil
}

// BlockHash implements btcwatch.Source.
func (c *Chain) BlockHash(_ context.Context, height int64) (chainhash.Hash, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Fail != nil {
		return chainhash.Hash{}, c.Fail
	}
	if height < 0 || height >= int64(len(c.blocks)) {
		return chainhash.Hash{}, fmt.Errorf("block height %d out of range", height)
	}
	return c.blocks[height].BlockHash(), nil
}

// Block implements btcwatch.Source.
func (c *Chain) Block(_ context.Context, hash chainhash.Hash) (*wire.MsgBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Fail != nil {
		return nil, c.Fail
	}
	for i := len(c.blocks) - 1; i >= 0; i-- {
		if c.blocks[i].BlockHash() == hash {
			return c.blocks[i], nil
		}
	}
	return nil, fmt.Errorf("block %s not found", hash)
}

// FeeRate implements btcwatch.Source.
func (c *Chain) FeeRate(context.Context) (uint64, error) { return FeeRate, nil }

// SendRawTransaction implements btcwatch.Source: it decodes raw and puts it in the
// mempool unless it is already there or in a block. A transaction spending an input of
// a mempool transaction replaces it (RBF). Scripts are not checked.
func (c *Chain) SendRawTransaction(_ context.Context, raw []byte) error {
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Fail != nil {
		return c.Fail
	}
	c.Submissions++
	id := tx.TxHash()
	for _, b := range c.blocks {
		for _, t := range b.Transactions {
			if t.TxHash() == id {
				return nil
			}
		}
	}
	keep := c.mempool[:0]
	for _, t := range c.mempool {
		if t.TxHash() == id {
			return nil
		}
		if !conflicts(t, &tx) {
			keep = append(keep, t)
		}
	}
	c.mempool = append(keep, &tx)
	c.Sent = append(c.Sent, id)
	return nil
}

func conflicts(a, b *wire.MsgTx) bool {
	for _, x := range a.TxIn {
		for _, y := range b.TxIn {
			if x.PreviousOutPoint == y.PreviousOutPoint {
				return true
			}
		}
	}
	return false
}

// InMempool reports whether txid is in the mempool.
func (c *Chain) InMempool(txid chainhash.Hash) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.mempool {
		if t.TxHash() == txid {
			return true
		}
	}
	return false
}
