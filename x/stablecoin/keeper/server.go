package keeper

import (
	"context"

	"github.com/OWNER/keystone/x/stablecoin/types"
)

type msgServer struct{ k *Keeper }

// NewMsgServerImpl returns the Msg service.
func NewMsgServerImpl(k *Keeper) types.MsgServer { return msgServer{k} }

func (m msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if err := m.k.UpdateParams(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgUpdateParamsResponse{}, nil
}

type querier struct{ k *Keeper }

// NewQuerier returns the Query service.
func NewQuerier(k *Keeper) types.QueryServer { return querier{k} }

func (q querier) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	p, err := q.k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q querier) Surplus(ctx context.Context, _ *types.QuerySurplusRequest) (*types.QuerySurplusResponse, error) {
	return &types.QuerySurplusResponse{Surplus: q.k.SurplusBalance(ctx)}, nil
}

func (q querier) BadDebt(ctx context.Context, _ *types.QueryBadDebtRequest) (*types.QueryBadDebtResponse, error) {
	sin, err := q.k.GetSin(ctx)
	if err != nil {
		return nil, err
	}
	paused, err := q.k.AreMintsPaused(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryBadDebtResponse{Sin: sin, MintsPaused: paused}, nil
}
