package keeper

import (
	"context"

	"github.com/OWNER/keystone/x/poa/types"
)

type msgServer struct{ k *Keeper }

// NewMsgServerImpl returns the x/poa Msg service.
func NewMsgServerImpl(k *Keeper) types.MsgServer { return msgServer{k} }

var _ types.MsgServer = msgServer{}

func (m msgServer) SetValidators(ctx context.Context, msg *types.MsgSetValidators) (*types.MsgSetValidatorsResponse, error) {
	if err := m.k.SetValidators(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgSetValidatorsResponse{}, nil
}

func (m msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if err := m.k.UpdateParams(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgUpdateParamsResponse{}, nil
}
