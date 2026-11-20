package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/x/admin/types"
)

type querier struct{ k *Keeper }

// NewQuerier returns the x/admin Query service.
func NewQuerier(k *Keeper) types.QueryServer { return querier{k} }

var _ types.QueryServer = querier{}

func (q querier) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	p, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q querier) Allowlist(ctx context.Context, _ *types.QueryAllowlistRequest) (*types.QueryAllowlistResponse, error) {
	entries, err := q.k.AllEntries(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryAllowlistResponse{Allowlist: entries}, nil
}

func (q querier) AllowEntry(ctx context.Context, req *types.QueryAllowEntryRequest) (*types.QueryAllowEntryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	addr, err := sdk.AccAddressFromBech32(req.Operator)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	e, err := q.k.GetEntry(ctx, addr)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryAllowEntryResponse{Entry: e}, nil
}
