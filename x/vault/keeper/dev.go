package keeper

import (
	"context"

	"github.com/OWNER/keystone/x/vault/types"
)

// DevCreditCollateral credits collateral without a Bitcoin deposit (M1 only). The
// signer must be the x/poa admin. Its Msg handler is registered only in devcredit
// builds (dev_devcredit.go); the method itself is also used by tests.
func (k *Keeper) DevCreditCollateral(ctx context.Context, msg *types.MsgDevCreditCollateral) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.admin.CheckAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	_, err := k.CreditCollateral(ctx, msg.VaultId, msg.Sat)
	return err
}

type devMsgServer struct{ k *Keeper }

// NewDevMsgServerImpl returns the DevMsg service.
func NewDevMsgServerImpl(k *Keeper) types.DevMsgServer { return devMsgServer{k} }

func (m devMsgServer) DevCreditCollateral(ctx context.Context, msg *types.MsgDevCreditCollateral) (*types.MsgDevCreditCollateralResponse, error) {
	if err := m.k.DevCreditCollateral(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgDevCreditCollateralResponse{}, nil
}
