package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/sidecar/signer"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
)

// FrostView is the FROST signer's read-only view of the latest committed state (spec
// M4a-T6). It is safe to call from ExtendVote.
func (app *App) FrostView() signer.ChainView { return frostView{app} }

type frostView struct{ app *App }

func (v frostView) Load(mine map[uint64]uint32) (signer.View, error) {
	ctx, err := v.app.CreateQueryContextWithCheckHeader(0, false, false)
	if err != nil {
		return signer.View{}, err
	}
	return LoadFrostView(ctx, v.app, mine)
}

// LoadFrostView reads, for each epoch in mine (epoch → share index) that is ACTIVE or
// RETIRING, the share's nonce pool and the OPEN sessions assigned to it.
func LoadFrostView(ctx sdk.Context, app *App, mine map[uint64]uint32) (signer.View, error) {
	k := app.CustodyKeeper
	p, err := k.GetParams(ctx)
	if err != nil {
		return signer.View{}, err
	}
	view := signer.View{NoncePool: int(p.NoncePool)}
	sessions, err := k.GetActiveSessions(ctx)
	if err != nil {
		return signer.View{}, err
	}
	err = k.Epochs.Walk(ctx, nil, func(n uint64, e custodytypes.KeyEpoch) (bool, error) {
		idx, ok := mine[n]
		if ok && e.Status == custodytypes.EPOCH_STATUS_RETIRED {
			view.Retired = append(view.Retired, n)
		}
		if !ok || (e.Status != custodytypes.EPOCH_STATUS_ACTIVE && e.Status != custodytypes.EPOCH_STATUS_RETIRING) {
			return false, nil
		}
		ev := signer.EpochView{Number: n, GroupKey: e.GroupKey, Index: idx}
		if ev.LastRecorded, ev.Pool, err = k.NoncePool(ctx, n, idx); err != nil {
			return true, err
		}
		for _, s := range sessions {
			if s.Epoch != n || s.State != custodytypes.SESSION_STATE_OPEN {
				continue
			}
			for _, a := range s.Signers {
				if a.ShareIndex == idx {
					ev.Sessions = append(ev.Sessions, s)
					break
				}
			}
		}
		view.Epochs = append(view.Epochs, ev)
		return false, nil
	})
	return view, err
}
