package keeper

import (
	"context"

	"github.com/logicvoid0x/keystone/x/admin/types"
)

type msgServer struct{ k *Keeper }

// NewMsgServerImpl returns the x/admin Msg service.
func NewMsgServerImpl(k *Keeper) types.MsgServer { return msgServer{k} }

var _ types.MsgServer = msgServer{}

func (m msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if err := m.k.UpdateParams(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgUpdateParamsResponse{}, nil
}

func (m msgServer) SetAllowlist(ctx context.Context, msg *types.MsgSetAllowlist) (*types.MsgSetAllowlistResponse, error) {
	if err := m.k.SetAllowlist(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgSetAllowlistResponse{}, nil
}

func (m msgServer) Exec(ctx context.Context, msg *types.MsgExec) (*types.MsgExecResponse, error) {
	if err := m.k.Exec(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgExecResponse{}, nil
}
