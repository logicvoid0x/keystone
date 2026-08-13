package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/vault/types"
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

func (m msgServer) OpenVault(ctx context.Context, msg *types.MsgOpenVault) (*types.MsgOpenVaultResponse, error) {
	id, addr, err := m.k.OpenVault(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgOpenVaultResponse{VaultId: id, Address: addr}, nil
}

func (m msgServer) Mint(ctx context.Context, msg *types.MsgMint) (*types.MsgMintResponse, error) {
	if err := m.k.Mint(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgMintResponse{}, nil
}

func (m msgServer) Repay(ctx context.Context, msg *types.MsgRepay) (*types.MsgRepayResponse, error) {
	r, err := m.k.Repay(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgRepayResponse{Repaid: r}, nil
}

func (m msgServer) WithdrawCollateral(ctx context.Context, msg *types.MsgWithdrawCollateral) (*types.MsgWithdrawCollateralResponse, error) {
	id, err := m.k.WithdrawCollateral(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgWithdrawCollateralResponse{RequestId: id}, nil
}

func (m msgServer) CloseVault(ctx context.Context, msg *types.MsgCloseVault) (*types.MsgCloseVaultResponse, error) {
	id, err := m.k.CloseVault(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgCloseVaultResponse{RequestId: id}, nil
}

func (m msgServer) DepositClaim(ctx context.Context, msg *types.MsgDepositClaim) (*types.MsgDepositClaimResponse, error) {
	if err := m.k.DepositClaim(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgDepositClaimResponse{}, nil
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

func (q querier) withDebt(ctx context.Context, v types.Vault) (types.VaultWithDebt, error) {
	d, err := q.k.Debt(ctx, v)
	if err != nil {
		return types.VaultWithDebt{}, err
	}
	return types.VaultWithDebt{Vault: v, Debt: d}, nil
}

func (q querier) Vault(ctx context.Context, req *types.QueryVaultRequest) (*types.QueryVaultResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	v, err := q.k.GetVault(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	wd, err := q.withDebt(ctx, v)
	if err != nil {
		return nil, err
	}
	return &types.QueryVaultResponse{Vault: wd}, nil
}

func (q querier) VaultsByOwner(ctx context.Context, req *types.QueryVaultsByOwnerRequest) (*types.QueryVaultsByOwnerResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	owner, err := sdk.AccAddressFromBech32(req.Owner)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	var out []types.VaultWithDebt
	err = q.k.OwnerIndex.Walk(ctx, collections.NewPrefixedPairRange[sdk.AccAddress, uint64](owner),
		func(key collections.Pair[sdk.AccAddress, uint64]) (bool, error) {
			v, err := q.k.GetVault(ctx, key.K2())
			if err != nil {
				return true, err
			}
			wd, err := q.withDebt(ctx, v)
			if err != nil {
				return true, err
			}
			out = append(out, wd)
			return false, nil
		})
	if err != nil {
		return nil, err
	}
	return &types.QueryVaultsByOwnerResponse{Vaults: out}, nil
}

func (q querier) RateState(ctx context.Context, _ *types.QueryRateStateRequest) (*types.QueryRateStateResponse, error) {
	rs, err := q.k.RateState.Get(ctx)
	if err != nil {
		return nil, err
	}
	a, err := q.k.TotalArt.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryRateStateResponse{RateState: rs, TotalArt: a}, nil
}

func (q querier) LiquidationQueue(ctx context.Context, req *types.QueryLiquidationQueueRequest) (*types.QueryLiquidationQueueResponse, error) {
	limit := uint32(50)
	if req != nil && req.Limit > 0 {
		limit = min(req.Limit, 500)
	}
	var out []types.VaultWithDebt
	err := q.k.LiqIndex.Walk(ctx, nil, func(key collections.Pair[[]byte, uint64], _ bool) (bool, error) {
		v, err := q.k.GetVault(ctx, key.K2())
		if err != nil {
			return true, err
		}
		wd, err := q.withDebt(ctx, v)
		if err != nil {
			return true, err
		}
		out = append(out, wd)
		return uint32(len(out)) >= limit, nil
	})
	if err != nil {
		return nil, err
	}
	return &types.QueryLiquidationQueueResponse{Vaults: out}, nil
}
