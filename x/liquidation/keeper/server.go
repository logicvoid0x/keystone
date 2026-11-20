package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/x/liquidation/types"
)

type msgServer struct{ k *Keeper }

// NewMsgServerImpl returns the Msg service.
func NewMsgServerImpl(k *Keeper) types.MsgServer { return msgServer{k} }

var _ types.MsgServer = msgServer{}

func (m msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if err := m.k.UpdateParams(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgUpdateParamsResponse{}, nil
}

func (m msgServer) DepositPool(ctx context.Context, msg *types.MsgDepositPool) (*types.MsgDepositPoolResponse, error) {
	if err := m.k.DepositPool(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgDepositPoolResponse{}, nil
}

func (m msgServer) WithdrawPool(ctx context.Context, msg *types.MsgWithdrawPool) (*types.MsgWithdrawPoolResponse, error) {
	w, g, err := m.k.WithdrawPool(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgWithdrawPoolResponse{Withdrawn: w, GainSat: g}, nil
}

func (m msgServer) TakeCollateral(ctx context.Context, msg *types.MsgTakeCollateral) (*types.MsgTakeCollateralResponse, error) {
	sat, paid, err := m.k.TakeCollateral(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgTakeCollateralResponse{Sat: sat, Paid: paid}, nil
}

type querier struct{ k *Keeper }

// NewQuerier returns the Query service.
func NewQuerier(k *Keeper) types.QueryServer { return querier{k} }

var _ types.QueryServer = querier{}

func (q querier) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	p, err := q.k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: p}, nil
}

func (q querier) Pool(ctx context.Context, _ *types.QueryPoolRequest) (*types.QueryPoolResponse, error) {
	t, err := q.k.TotalDeposits.Get(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := q.k.PendingSat.Get(ctx)
	if err != nil {
		return nil, err
	}
	sc, err := q.k.Scalars.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryPoolResponse{TotalDeposits: t, PendingSat: ps, Scalars: sc}, nil
}

func (q querier) PoolDeposit(ctx context.Context, req *types.QueryPoolDepositRequest) (*types.QueryPoolDepositResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	c, g, err := q.k.Position(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &types.QueryPoolDepositResponse{Compounded: c, PendingGainSat: g}, nil
}

func (q querier) withPrice(ctx context.Context, a types.Auction) (types.AuctionWithPrice, error) {
	p, err := q.k.Params.Get(ctx)
	if err != nil {
		return types.AuctionWithPrice{}, err
	}
	return types.AuctionWithPrice{Auction: a, Price: AuctionPrice(a, sdk.UnwrapSDKContext(ctx).BlockHeight(), p)}, nil
}

func (q querier) Auction(ctx context.Context, req *types.QueryAuctionRequest) (*types.QueryAuctionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	a, err := q.k.GetAuction(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	wp, err := q.withPrice(ctx, a)
	if err != nil {
		return nil, err
	}
	return &types.QueryAuctionResponse{Auction: wp}, nil
}

func (q querier) Auctions(ctx context.Context, _ *types.QueryAuctionsRequest) (*types.QueryAuctionsResponse, error) {
	var out []types.AuctionWithPrice
	err := q.k.Auctions.Walk(ctx, nil, func(_ uint64, a types.Auction) (bool, error) {
		wp, err := q.withPrice(ctx, a)
		if err != nil {
			return true, err
		}
		out = append(out, wp)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return &types.QueryAuctionsResponse{Auctions: out}, nil
}
