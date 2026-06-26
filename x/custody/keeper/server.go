package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/custody/types"
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

func (q querier) Claimable(ctx context.Context, req *types.QueryClaimableRequest) (*types.QueryClaimableResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	v, err := q.k.GetClaimable(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &types.QueryClaimableResponse{ClaimableSat: v}, nil
}

func (q querier) DepositAddress(ctx context.Context, req *types.QueryDepositAddressRequest) (*types.QueryDepositAddressResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	addr, script, epoch, err := q.k.DepositAddress(ctx, req.VaultId)
	if errors.Is(err, types.ErrNoEpoch) || errors.Is(err, types.ErrNotFound) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &types.QueryDepositAddressResponse{Address: addr, PkScript: script, Epoch: epoch}, nil
}

func (q querier) ActiveEpoch(ctx context.Context, _ *types.QueryActiveEpochRequest) (*types.QueryActiveEpochResponse, error) {
	e, ok, err := q.k.GetActiveEpoch(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, status.Error(codes.NotFound, types.ErrNoEpoch.Error())
	}
	addr, err := q.k.TreasuryAddress(ctx, e)
	if err != nil {
		return nil, err
	}
	return &types.QueryActiveEpochResponse{Epoch: e, TreasuryAddress: addr}, nil
}

func (q querier) Treasury(ctx context.Context, _ *types.QueryTreasuryRequest) (*types.QueryTreasuryResponse, error) {
	p, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	res := &types.QueryTreasuryResponse{CapSat: p.TreasuryCapSat}
	if res.CustodiedSat, err = q.k.UtxoSum(ctx); err != nil {
		return nil, err
	}
	if res.CreditedSat, err = q.k.Credited.Get(ctx); err != nil {
		return nil, err
	}
	if res.FeeReserveSat, err = q.k.FeeReserve.Get(ctx); err != nil {
		return nil, err
	}
	if res.DeficitSat, err = q.k.Deficit.Get(ctx); err != nil {
		return nil, err
	}
	anchor, err := q.k.Anchor.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	res.Anchor = anchor
	return res, nil
}
