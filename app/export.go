package app

import (
	"encoding/json"
	"errors"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"

	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

// ExportAppStateAndValidators exports state and the active x/poa operators. Zero-height
// export is not supported: there is no staking state to reset.
func (app *App) ExportAppStateAndValidators(forZeroHeight bool, _, modulesToExport []string) (servertypes.ExportedApp, error) {
	if forZeroHeight {
		return servertypes.ExportedApp{}, errors.New("zero-height export is not supported")
	}
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight()})

	genState, err := app.ModuleManager.ExportGenesisForModules(ctx, app.appCodec, modulesToExport)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	appState, err := json.MarshalIndent(genState, "", "  ")
	if err != nil {
		return servertypes.ExportedApp{}, err
	}

	active, err := app.PoaKeeper.ActiveValidators(ctx)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	vals := make([]cmttypes.GenesisValidator, 0, len(active))
	for _, op := range active {
		pk := ed25519.PubKey(op.ConsensusPubkey)
		vals = append(vals, cmttypes.GenesisValidator{
			Address: pk.Address(), PubKey: pk, Power: op.Power, Name: op.Moniker,
		})
	}
	return servertypes.ExportedApp{
		AppState:        appState,
		Validators:      vals,
		Height:          app.LastBlockHeight() + 1,
		ConsensusParams: app.GetConsensusParams(ctx),
	}, nil
}
