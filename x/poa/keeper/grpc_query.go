package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/poa/types"
)

type querier struct{ k *Keeper }

// NewQuerier returns the x/poa Query service.
func NewQuerier(k *Keeper) types.QueryServer { return querier{k} }

var _ types.QueryServer = querier{}

func (q querier) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	p, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q querier) Validators(ctx context.Context, _ *types.QueryValidatorsRequest) (*types.QueryValidatorsResponse, error) {
	ops, err := q.k.AllOperators(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryValidatorsResponse{Validators: ops}, nil
}

func (q querier) Validator(ctx context.Context, req *types.QueryValidatorRequest) (*types.QueryValidatorResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	op, err := q.k.GetOperator(ctx, addr)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryValidatorResponse{Validator: op}, nil
}

func (q querier) TotalPower(ctx context.Context, _ *types.QueryTotalPowerRequest) (*types.QueryTotalPowerResponse, error) {
	p, err := q.k.TotalPower(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryTotalPowerResponse{TotalPower: p}, nil
}
