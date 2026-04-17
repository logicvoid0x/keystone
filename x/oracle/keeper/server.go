package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/oracle/types"
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

func (q querier) Price(ctx context.Context, _ *types.QueryPriceRequest) (*types.QueryPriceResponse, error) {
	ps, err := q.k.GetPriceState(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryPriceResponse{Price: ps}, nil
}

func (q querier) Twap(ctx context.Context, _ *types.QueryTwapRequest) (*types.QueryTwapResponse, error) {
	ps, err := q.k.GetPriceState(ctx)
	if err != nil {
		return nil, err
	}
	meta, err := q.k.TwapMeta.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryTwapResponse{TwapMicroUsd: ps.TwapMicroUsd, Blocks: meta.Count}, nil
}

func (q querier) MissCounter(ctx context.Context, req *types.QueryMissCounterRequest) (*types.QueryMissCounterResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	if _, err := sdk.AccAddressFromBech32(req.Validator); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	ops, err := q.k.vals.ActiveValidators(ctx)
	if err != nil {
		return nil, err
	}
	p, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		if op.OperatorAddress == req.Validator {
			n, err := q.k.Misses(ctx, op.ConsAddress())
			if err != nil {
				return nil, err
			}
			return &types.QueryMissCounterResponse{Misses: n, Window: p.MissWindow}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "not an active operator")
}
