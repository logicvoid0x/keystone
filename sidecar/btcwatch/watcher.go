// Package btcwatch is the Bitcoin watcher sidecar (design §9.7, spec M3-T3). It follows
// one bitcoind, keeps the last ScanWindow blocks of its best chain, scans them for
// outputs paying registered deposit scripts and builds the BitcoinPayload that
// ExtendVote attaches to the node's vote extension. It is not state-machine code: it
// does network I/O and runs in its own goroutine. Its output only reaches state
// through vote extensions, where more than 2/3 of power must agree.
package btcwatch

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"

	"cosmossdk.io/log/v2"

	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/app/protocol/vetypes"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// Source is the Bitcoin node the watcher follows.
type Source interface {
	TipHeight(ctx context.Context) (int64, error)
	BlockHash(ctx context.Context, height int64) (chainhash.Hash, error)
	Block(ctx context.Context, hash chainhash.Hash) (*wire.MsgBlock, error)
	// FeeRate is the current estimate in sat/vB.
	FeeRate(ctx context.Context) (uint64, error)
}

// Outpoint identifies a Bitcoin output (txid in internal byte order).
type Outpoint struct {
	Txid chainhash.Hash
	Vout uint32
}

// Record is an accepted deposit's block, from committed state.
type Record struct {
	Height uint32
	Hash   chainhash.Hash
}

// View is the part of committed Keystone state the watcher needs, refreshed every
// update (design §9.7 step 1).
type View struct {
	Network      string
	ConfirmDepth uint32
	// Scripts are the registered deposit scripts.
	Scripts map[string]bool
	// Deposits are the accepted deposits whose UTXO Keystone still holds.
	Deposits map[Outpoint]Record
}

// ChainView loads a View from the latest committed state (read-only).
type ChainView interface {
	Load() (View, error)
}

// Config tunes a Watcher.
type Config struct {
	// Network must match the chain's custody bitcoin_network param.
	Network string
	// ScanWindow is the number of recent blocks kept and scanned.
	ScanWindow int64
	// PollInterval is how often Run polls bitcoind (ZMQ triggers polls sooner).
	PollInterval time.Duration
}

// Defaults.
const (
	DefaultScanWindow   = 1000
	DefaultPollInterval = 2 * time.Second
)

type output struct {
	txid   chainhash.Hash
	vout   uint32
	amount uint64
	script []byte
}

type block struct {
	height int64
	hash   chainhash.Hash
	outs   []output // P2TR outputs only
}

// Watcher follows a Source and produces snapshots.
type Watcher struct {
	src    Source
	view   ChainView
	cfg    Config
	logger log.Logger

	mu     sync.Mutex
	blocks []block // contiguous, ascending, ending at the best tip
	snap   *vetypes.BitcoinPayload
}

// New returns a watcher; call Update (or Run) to start following src.
func New(src Source, view ChainView, cfg Config, logger log.Logger) *Watcher {
	if cfg.ScanWindow <= 0 {
		cfg.ScanWindow = DefaultScanWindow
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if logger == nil {
		logger = log.NewNopLogger()
	}
	return &Watcher{src: src, view: view, cfg: cfg, logger: logger}
}

// Snapshot returns the latest payload for ExtendVote. It never blocks on I/O; ok is
// false when the watcher has no fresh view of Bitcoin.
func (w *Watcher) Snapshot() (*vetypes.BitcoinPayload, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.snap, w.snap != nil
}

// Update syncs the best chain, reloads the committed view and rebuilds the snapshot.
// On any error the snapshot is cleared, so a node that cannot see Bitcoin attests
// nothing.
func (w *Watcher) Update(ctx context.Context) error {
	if err := w.sync(ctx); err != nil {
		w.setSnap(nil)
		return fmt.Errorf("sync: %w", err)
	}
	v, err := w.view.Load()
	if err != nil {
		w.setSnap(nil)
		return fmt.Errorf("load committed view: %w", err)
	}
	if v.Network != w.cfg.Network {
		w.setSnap(nil)
		return fmt.Errorf("watcher network %q does not match chain network %q", w.cfg.Network, v.Network)
	}
	fee, err := w.src.FeeRate(ctx)
	if err != nil {
		w.setSnap(nil)
		return fmt.Errorf("fee rate: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.snap = w.build(v, fee)
	return nil
}

func (w *Watcher) setSnap(p *vetypes.BitcoinPayload) {
	w.mu.Lock()
	w.snap = p
	w.mu.Unlock()
}

// Run calls Update every PollInterval and whenever trigger fires, until ctx ends.
func (w *Watcher) Run(ctx context.Context, trigger <-chan struct{}) {
	t := time.NewTicker(w.cfg.PollInterval)
	defer t.Stop()
	var lastErr string
	for {
		if err := w.Update(ctx); err != nil {
			if err.Error() != lastErr {
				w.logger.Error("bitcoin watcher update failed", "err", err)
			}
			lastErr = err.Error()
		} else if lastErr != "" {
			w.logger.Info("bitcoin watcher recovered")
			lastErr = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-trigger:
		}
	}
}

var errReorgDuringSync = errors.New("best chain changed during sync")

// sync brings blocks up to the source's tip, dropping blocks no longer on its best
// chain.
func (w *Watcher) sync(ctx context.Context) error {
	tip, err := w.src.TipHeight(ctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	blocks := slices.Clone(w.blocks)
	w.mu.Unlock()

	// Fork point: the highest stored block the source still has at its height.
	keep := len(blocks)
	for keep > 0 {
		b := blocks[keep-1]
		if b.height <= tip {
			h, err := w.src.BlockHash(ctx, b.height)
			if err != nil {
				return err
			}
			if h == b.hash {
				break
			}
		}
		keep--
	}
	blocks = blocks[:keep]
	start := max(tip-w.cfg.ScanWindow+1, 0)
	for len(blocks) > 0 && blocks[0].height < start {
		blocks = blocks[1:]
	}
	next := start
	if len(blocks) > 0 {
		next = blocks[len(blocks)-1].height + 1
	}
	for h := next; h <= tip; h++ {
		hash, err := w.src.BlockHash(ctx, h)
		if err != nil {
			return err
		}
		mb, err := w.src.Block(ctx, hash)
		if err != nil {
			return err
		}
		if mb.BlockHash() != hash {
			return fmt.Errorf("block %d: hash mismatch", h)
		}
		if len(blocks) > 0 && mb.Header.PrevBlock != blocks[len(blocks)-1].hash {
			w.store(blocks)
			return errReorgDuringSync
		}
		blocks = append(blocks, scan(h, hash, mb))
	}
	w.store(blocks)
	return nil
}

func (w *Watcher) store(blocks []block) {
	w.mu.Lock()
	w.blocks = blocks
	w.mu.Unlock()
}

func scan(height int64, hash chainhash.Hash, mb *wire.MsgBlock) block {
	b := block{height: height, hash: hash}
	for _, tx := range mb.Transactions {
		txid := tx.TxHash()
		for i, out := range tx.TxOut {
			if out.Value > 0 && custodytypes.IsP2TRScript(out.PkScript) {
				b.outs = append(b.outs, output{txid: txid, vout: uint32(i), amount: uint64(out.Value), script: slices.Clone(out.PkScript)})
			}
		}
	}
	return b
}

// at returns the stored block at height, or nil.
func (w *Watcher) at(height int64) *block {
	if len(w.blocks) == 0 {
		return nil
	}
	i := height - w.blocks[0].height
	if i < 0 || i >= int64(len(w.blocks)) {
		return nil
	}
	return &w.blocks[i]
}

func ref(height int64, hash chainhash.Hash) *vetypes.BtcBlockRef {
	return &vetypes.BtcBlockRef{Height: uint32(height), Hash: slices.Clone(hash[:])}
}

// build is the §9.7 BitcoinPayload: anchor = block at tip − k; deposits to registered
// scripts with at least k confirmations not yet accepted, oldest first, at most
// MaxDeposits; ReorgSeen for accepted deposits whose block is no longer at its height
// on the best chain. Lists are in canonical order. Caller holds w.mu.
func (w *Watcher) build(v View, fee uint64) *vetypes.BitcoinPayload {
	if len(w.blocks) == 0 {
		return nil
	}
	tip := w.blocks[len(w.blocks)-1].height
	k := int64(max(v.ConfirmDepth, 1))
	p := &vetypes.BitcoinPayload{FeerateSatVb: fee}
	if b := w.at(tip - k); b != nil {
		p.Anchor = ref(b.height, b.hash)
	}

	for _, b := range w.blocks {
		if b.height > tip-k+1 || len(p.Deposits) == protocol.MaxDeposits {
			break
		}
		outs := slices.Clone(b.outs)
		slices.SortFunc(outs, func(x, y output) int { return cmp.Or(bytes.Compare(x.txid[:], y.txid[:]), cmp.Compare(x.vout, y.vout)) })
		for _, o := range outs {
			if len(p.Deposits) == protocol.MaxDeposits {
				break
			}
			if !v.Scripts[string(o.script)] {
				continue
			}
			if _, accepted := v.Deposits[Outpoint{o.txid, o.vout}]; accepted {
				continue
			}
			p.Deposits = append(p.Deposits, vetypes.DepositSeen{
				Txid: slices.Clone(o.txid[:]), Vout: o.vout, AmountSat: o.amount, PkScript: o.script, Block: ref(b.height, b.hash),
			})
		}
	}

	ops := make([]Outpoint, 0, len(v.Deposits))
	for op := range v.Deposits {
		ops = append(ops, op)
	}
	slices.SortFunc(ops, func(x, y Outpoint) int { return cmp.Or(bytes.Compare(x.Txid[:], y.Txid[:]), cmp.Compare(x.Vout, y.Vout)) })
	for _, op := range ops {
		if len(p.Reorgs) == protocol.MaxReorgs {
			break
		}
		rec := v.Deposits[op]
		b := w.at(int64(rec.Height))
		if b == nil || b.hash == rec.Hash {
			continue // outside the window, above our tip, or still on the best chain
		}
		p.Reorgs = append(p.Reorgs, vetypes.ReorgSeen{Txid: slices.Clone(op.Txid[:]), Vout: op.Vout, OldBlockHash: slices.Clone(rec.Hash[:])})
	}
	ve := vetypes.VoteExtension{Bitcoin: p}
	protocol.Canonicalize(&ve)
	return p
}
