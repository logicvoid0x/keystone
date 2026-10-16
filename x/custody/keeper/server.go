package keeper

import (
	"context"
	"encoding/hex"
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
func (m msgServer) ClearTheftPause(ctx context.Context, msg *types.MsgClearTheftPause) (*types.MsgClearTheftPauseResponse, error) {
	if err := m.k.ClearTheftPause(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgClearTheftPauseResponse{}, nil
}


func (m msgServer) WithdrawClaim(ctx context.Context, msg *types.MsgWithdrawClaim) (*types.MsgWithdrawClaimResponse, error) {
	id, err := m.k.WithdrawClaim(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgWithdrawClaimResponse{RequestId: id}, nil
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

func (q querier) Withdrawal(ctx context.Context, req *types.QueryWithdrawalRequest) (*types.QueryWithdrawalResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	r, err := q.k.Requests.Get(ctx, req.Id)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "withdrawal request %d", req.Id)
	} else if err != nil {
		return nil, err
	}
	return &types.QueryWithdrawalResponse{Request: r}, nil
}

func (q querier) Batch(ctx context.Context, req *types.QueryBatchRequest) (*types.QueryBatchResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	b, err := q.k.Batches.Get(ctx, req.Id)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "batch %d", req.Id)
	} else if err != nil {
		return nil, err
	}
	cur := b.Txs[len(b.Txs)-1]
	return &types.QueryBatchResponse{Batch: b, Txid: types.TxidString(cur.Txid), SignedTxHex: hex.EncodeToString(cur.SignedTx)}, nil
}

func (q querier) Batches(ctx context.Context, _ *types.QueryBatchesRequest) (*types.QueryBatchesResponse, error) {
	res := &types.QueryBatchesResponse{}
	err := q.k.Batches.Walk(ctx, nil, func(_ uint64, b types.WithdrawalBatch) (bool, error) {
		res.Batches = append(res.Batches, b)
		return false, nil
	})
	return res, err
}

func (q querier) Session(ctx context.Context, req *types.QuerySessionRequest) (*types.QuerySessionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	id, err := hex.DecodeString(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	s, err := q.k.Sessions.Get(ctx, id)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "session %s", req.Id)
	} else if err != nil {
		return nil, err
	}
	return &types.QuerySessionResponse{Session: s}, nil
}

func (q querier) ActiveSessions(ctx context.Context, _ *types.QueryActiveSessionsRequest) (*types.QueryActiveSessionsResponse, error) {
	ss, err := q.k.GetActiveSessions(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryActiveSessionsResponse{Sessions: ss}, nil
}

func (q querier) NoncePool(ctx context.Context, req *types.QueryNoncePoolRequest) (*types.QueryNoncePoolResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	last, cs, err := q.k.NoncePool(ctx, req.Epoch, req.ShareIndex)
	if err != nil {
		return nil, err
	}
	return &types.QueryNoncePoolResponse{LastRecorded: last, Commitments: cs}, nil
}

func (m msgServer) RegisterCustodyKey(ctx context.Context, msg *types.MsgRegisterCustodyKey) (*types.MsgRegisterCustodyKeyResponse, error) {
	if err := m.k.RegisterCustodyKey(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgRegisterCustodyKeyResponse{}, nil
}

func (m msgServer) DKGCommit(ctx context.Context, msg *types.MsgDKGCommit) (*types.MsgDKGCommitResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}
	if err := m.k.DKGCommit(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgDKGCommitResponse{}, nil
}

func (m msgServer) DKGDeal(ctx context.Context, msg *types.MsgDKGDeal) (*types.MsgDKGDealResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}
	if err := m.k.DKGDeal(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgDKGDealResponse{}, nil
}

func (m msgServer) DKGComplaint(ctx context.Context, msg *types.MsgDKGComplaint) (*types.MsgDKGComplaintResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}
	upheld, err := m.k.DKGComplaint(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgDKGComplaintResponse{Upheld: upheld}, nil
}

func (q querier) DKG(ctx context.Context, req *types.QueryDKGRequest) (*types.QueryDKGResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	run, ok, err := q.k.GetDKG(ctx, req.Epoch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no DKG for epoch %d", req.Epoch)
	}
	return &types.QueryDKGResponse{State: run.State, Dealers: run.Dealers, Complaints: run.Complaints}, nil
}

func (q querier) CustodyKeys(ctx context.Context, _ *types.QueryCustodyKeysRequest) (*types.QueryCustodyKeysResponse, error) {
	res := &types.QueryCustodyKeysResponse{}
	err := q.k.CustodyKeys.Walk(ctx, nil, func(_ sdk.AccAddress, ck types.CustodyKey) (bool, error) {
		res.Keys = append(res.Keys, ck)
		return false, nil
	})
	return res, err
}

func (q querier) PendingMembership(ctx context.Context, _ *types.QueryPendingMembershipRequest) (*types.QueryPendingMembershipResponse, error) {
	p, err := q.k.Pending.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return &types.QueryPendingMembershipResponse{}, nil
	} else if err != nil {
		return nil, err
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryPendingMembershipResponse{Pending: &p, EffectiveHeight: p.SinceHeight + int64(params.MembershipDelayBlocks)}, nil
}
