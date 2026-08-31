package app

import (
	"errors"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/sidecar/dkg"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// DKGView is the DKG participant's read-only view of the latest committed state (spec
// M4b-T2).
func (app *App) DKGView() dkg.ChainView { return dkgView{app} }

type dkgView struct{ app *App }

func (v dkgView) Load(op sdk.AccAddress) (dkg.View, error) {
	ctx, err := v.app.CreateQueryContextWithCheckHeader(0, false, false)
	if err != nil {
		return dkg.View{}, err
	}
	return LoadDKGView(ctx, v.app, op)
}

// LoadDKGView reads the latest DKG run, the custody keys and, once the run's epoch
// exists, op's verifying share in it.
func LoadDKGView(ctx sdk.Context, app *App, op sdk.AccAddress) (dkg.View, error) {
	k := app.CustodyKeeper
	// The chain id from the app: a query context may not carry it.
	view := dkg.View{ChainID: app.ChainID(), Height: ctx.BlockHeight()}
	err := k.CustodyKeys.Walk(ctx, nil, func(_ sdk.AccAddress, ck custodytypes.CustodyKey) (bool, error) {
		view.Keys = append(view.Keys, ck)
		return false, nil
	})
	if err != nil {
		return view, err
	}
	var latest uint64
	rng := new(collections.Range[uint64]).Descending()
	if err := k.DKGStates.Walk(ctx, rng, func(n uint64, _ custodytypes.DKGState) (bool, error) {
		latest = n
		return true, nil
	}); err != nil || latest == 0 {
		return view, err
	}
	run, _, err := k.GetDKG(ctx, latest)
	if err != nil {
		return view, err
	}
	view.State, view.Dealers, view.Complaints = &run.State, run.Dealers, run.Complaints
	e, err := k.Epochs.Get(ctx, latest)
	if errors.Is(err, collections.ErrNotFound) {
		return view, nil
	} else if err != nil {
		return view, err
	}
	view.Epoch = &e
	idx, err := k.ShareIndex.Get(ctx, collections.Join(latest, op))
	if errors.Is(err, collections.ErrNotFound) {
		return view, nil
	} else if err != nil {
		return view, err
	}
	view.VerifyingShare, err = k.VerifyingShares.Get(ctx, collections.Join(latest, idx))
	return view, err
}
