package app

import (
	"github.com/btcsuite/btcd/chainhash/v2"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/sidecar/btcwatch"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// BitcoinView is the Bitcoin watcher's read-only view of the latest committed state
// (spec M3-T3): network, confirmation depth, the deposit script registry and the
// accepted deposits still held. It is safe to call from the watcher's goroutine.
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
		Network: p.BitcoinNetwork, ConfirmDepth: p.ConfirmDepth,
		Scripts: map[string]bool{}, Deposits: map[btcwatch.Outpoint]btcwatch.Record{},
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
	return view, nil
}
