package app

import (
	"github.com/btcsuite/btcd/chainhash/v2"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/sidecar/btcwatch"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// BitcoinView is the Bitcoin watcher's read-only view of the latest committed state
// (spec M3-T3): network, confirmation depth, the deposit script registry, the accepted
// deposits still held and (M4a) the in-flight withdrawal transactions. It is safe to call from the watcher's goroutine.
func (app *App) BitcoinView() btcwatch.ChainView { return bitcoinView{app} }

type bitcoinView struct{ app *App }

func (v bitcoinView) Load() (btcwatch.View, error) {
	ctx, err := v.app.CreateQueryContextWithCheckHeader(0, false, false)
	if err != nil {
		return btcwatch.View{}, err
	}
	return LoadBitcoinView(ctx, v.app)
}

// LoadBitcoinView reads the watcher's view from ctx.
func LoadBitcoinView(ctx sdk.Context, app *App) (btcwatch.View, error) {
	k := app.CustodyKeeper
	p, err := k.GetParams(ctx)
	if err != nil {
		return btcwatch.View{}, err
	}
	view := btcwatch.View{
		Network: p.BitcoinNetwork, ConfirmDepth: p.ConfirmDepth, Height: ctx.BlockHeight(),
		Scripts: map[string]bool{}, Deposits: map[btcwatch.Outpoint]btcwatch.Record{},
		Utxos: map[btcwatch.Outpoint]bool{}, BatchTxids: map[chainhash.Hash]bool{},
	}
	err = k.ScriptVault.Walk(ctx, nil, func(script []byte, _ uint64) (bool, error) {
		view.Scripts[string(script)] = true
		return false, nil
	})
	if err != nil {
		return btcwatch.View{}, err
	}
	err = k.DepositRecords(ctx, func(txid []byte, vout uint32, rec custodytypes.DepositRecord) bool {
		var id, bh chainhash.Hash
		if len(txid) != chainhash.HashSize || len(rec.Block.Hash) != chainhash.HashSize {
			return false
		}
		copy(id[:], txid)
		copy(bh[:], rec.Block.Hash)
		view.Deposits[btcwatch.Outpoint{Txid: id, Vout: vout}] = btcwatch.Record{Height: rec.Block.Height, Hash: bh}
		return false
	})
	if err != nil {
		return btcwatch.View{}, err
	}
	// M7 (§9.12): every UTXO and every batch txid, for unauthorized-spend detection.
	err = k.Utxos.Walk(ctx, nil, func(op collections.Pair[[]byte, uint32], _ custodytypes.Utxo) (bool, error) {
		h, err := chainhash.NewHash(op.K1())
		if err != nil {
			return true, err
		}
		view.Utxos[btcwatch.Outpoint{Txid: *h, Vout: op.K2()}] = true
		return false, nil
	})
	if err != nil {
		return btcwatch.View{}, err
	}
	err = k.TxBatch.Walk(ctx, nil, func(txid []byte, _ uint64) (bool, error) {
		h, err := chainhash.NewHash(txid)
		if err != nil {
			return true, err
		}
		view.BatchTxids[*h] = true
		return false, nil
	})
	if err != nil {
		return btcwatch.View{}, err
	}
	err = k.InFlight.Walk(ctx, nil, func(_ uint64, id uint64) (bool, error) {
		b, err := k.Batches.Get(ctx, id)
		if err != nil {
			return true, err
		}
		for _, v := range b.Txs {
			h, err := chainhash.NewHash(v.Txid)
			if err != nil {
				return true, err
			}
			view.Watched = append(view.Watched, *h)
		}
		if cur := b.Txs[len(b.Txs)-1]; cur.SignedTx != nil {
			h, _ := chainhash.NewHash(cur.Txid)
			view.Signed = append(view.Signed, btcwatch.SignedTx{Txid: *h, Raw: cur.SignedTx})
		} else if len(b.Txs) > 1 {
			// An RBF version is being signed: keep the previous signed one out there.
			prev := b.Txs[len(b.Txs)-2]
			h, _ := chainhash.NewHash(prev.Txid)
			view.Signed = append(view.Signed, btcwatch.SignedTx{Txid: *h, Raw: prev.SignedTx})
		}
		return false, nil
	})
	if err != nil {
		return btcwatch.View{}, err
	}
	return view, nil
}
