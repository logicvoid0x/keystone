package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/x/custody/types"
)

// Keeper owns all satoshi accounting (design §10): key epochs, the deposit script
// registry, Bitcoin attestation tallies, UTXOs, claimable balances, the fee reserve, the
// custody deficit and the credited counter for the treasury cap. Credited = satoshis
// currently credited as vault collateral. Since M4a it also holds the
// withdrawal queue, batches, FROST signing sessions and nonce pools.
type Keeper struct {
	Schema          collections.Schema
	Params          collections.Item[types.Params]
	Epochs          collections.Map[uint64, types.KeyEpoch]
	ActiveEpoch     collections.Item[uint64]
	ScriptVault     collections.Map[[]byte, uint64]
	VaultScript     collections.Map[collections.Pair[uint64, uint64], []byte]
	Tallies         collections.Map[[]byte, types.Tally]
	TallyExpiry     collections.KeySet[collections.Pair[int64, []byte]]
	Anchor          collections.Item[vetypes.BtcBlockRef]
	Utxos           collections.Map[collections.Pair[[]byte, uint32], types.Utxo]
	UtxoIndex       collections.KeySet[collections.Triple[int32, uint64, []byte]]
	Deposits        collections.Map[collections.Pair[[]byte, uint32], types.DepositRecord]
	ShareIndex      collections.Map[collections.Pair[uint64, sdk.AccAddress], uint32]
	VerifyingShares collections.Map[collections.Pair[uint64, uint32], []byte]
	Claimable       collections.Map[sdk.AccAddress, sdkmath.Int]
	FeeReserve      collections.Item[sdkmath.Int]
	Deficit         collections.Item[sdkmath.Int]
	Credited        collections.Item[sdkmath.Int]
	Requests        collections.Map[uint64, types.WithdrawalRequest]
	NextRequest     collections.Sequence
	Queue           collections.KeySet[uint64]
	Batches         collections.Map[uint64, types.WithdrawalBatch]
	NextBatch       collections.Sequence
	TxBatch         collections.Map[[]byte, uint64]
	InFlight        collections.Map[uint64, uint64]
	Sessions        collections.Map[[]byte, types.SigningSession]
	ActiveSessions  collections.KeySet[[]byte]
	Commitments     collections.Map[collections.Triple[uint64, uint32, uint64], types.StoredCommitment]
	PoolHead        collections.Map[collections.Pair[uint64, uint32], uint64]
	SignerStats     collections.Map[collections.Pair[uint64, sdk.AccAddress], types.SignerStats]
	CustodyKeys     collections.Map[sdk.AccAddress, types.CustodyKey]
	HotKeys         collections.Map[sdk.AccAddress, []byte]
	DKGStates       collections.Map[uint64, types.DKGState]
	DKGMessages     collections.Map[collections.Triple[uint64, uint32, []byte], []byte]
	Pending         collections.Item[types.PendingMembership]
	SlashedEpochs   collections.Map[uint64, int64]
	TheftPaused     collections.Item[bool]

	admin   types.AdminKeeper
	vals    types.ValidatorSet
	vault   types.VaultKeeper
	slasher types.StakingSlasher
}

// NewKeeper builds the keeper. admin authorizes MsgUpdateParams (x/admin); vals gives
// attestation power (x/staking through app/valset). The vault keeper is attached with
// SetVaultKeeper, since
// x/vault also depends on x/custody.
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, admin types.AdminKeeper, vals types.ValidatorSet) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	outpoint := collections.PairKeyCodec(collections.BytesKey, collections.Uint32Key)
	k := &Keeper{
		Params:      collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Epochs:      collections.NewMap(sb, types.EpochsKey, "epochs", collections.Uint64Key, codec.CollValue[types.KeyEpoch](cdc)),
		ActiveEpoch: collections.NewItem(sb, types.ActiveEpochKey, "active_epoch", collections.Uint64Value),
		ScriptVault: collections.NewMap(sb, types.ScriptVaultKey, "script_vault", collections.BytesKey, collections.Uint64Value),
		VaultScript: collections.NewMap(sb, types.VaultScriptKey, "vault_script",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key), collections.BytesValue),
		Tallies: collections.NewMap(sb, types.TalliesKey, "tallies", collections.BytesKey, codec.CollValue[types.Tally](cdc)),
		TallyExpiry: collections.NewKeySet(sb, types.TallyExpiryKey, "tally_expiry",
			collections.PairKeyCodec(collections.Int64Key, collections.BytesKey)),
		Anchor:    collections.NewItem(sb, types.AnchorKey, "anchor", codec.CollValue[vetypes.BtcBlockRef](cdc)),
		Utxos:     collections.NewMap(sb, types.UtxosKey, "utxos", outpoint, codec.CollValue[types.Utxo](cdc)),
		UtxoIndex: collections.NewKeySet(sb, types.UtxoIndexKey, "utxo_index", collections.TripleKeyCodec(collections.Int32Key, collections.Uint64Key, collections.BytesKey)),
		Deposits:  collections.NewMap(sb, types.DepositsKey, "deposits", outpoint, codec.CollValue[types.DepositRecord](cdc)),
		ShareIndex: collections.NewMap(sb, types.ShareIndexKey, "share_index",
			collections.PairKeyCodec(collections.Uint64Key, sdk.AccAddressKey), collections.Uint32Value),
		VerifyingShares: collections.NewMap(sb, types.VerifyingSharesKey, "verifying_shares",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint32Key), collections.BytesValue),
		Claimable:      collections.NewMap(sb, types.ClaimableKey, "claimable", sdk.AccAddressKey, sdk.IntValue),
		FeeReserve:     collections.NewItem(sb, types.FeeReserveKey, "fee_reserve", sdk.IntValue),
		Deficit:        collections.NewItem(sb, types.DeficitKey, "deficit", sdk.IntValue),
		Credited:       collections.NewItem(sb, types.CreditedKey, "credited", sdk.IntValue),
		Requests:       collections.NewMap(sb, types.RequestsKey, "requests", collections.Uint64Key, codec.CollValue[types.WithdrawalRequest](cdc)),
		NextRequest:    collections.NewSequence(sb, types.NextRequestKey, "next_request"),
		Queue:          collections.NewKeySet(sb, types.QueueKey, "queue", collections.Uint64Key),
		Batches:        collections.NewMap(sb, types.BatchesKey, "batches", collections.Uint64Key, codec.CollValue[types.WithdrawalBatch](cdc)),
		NextBatch:      collections.NewSequence(sb, types.NextBatchKey, "next_batch"),
		TxBatch:        collections.NewMap(sb, types.TxBatchKey, "tx_batch", collections.BytesKey, collections.Uint64Value),
		InFlight:       collections.NewMap(sb, types.InFlightKey, "in_flight", collections.Uint64Key, collections.Uint64Value),
		Sessions:       collections.NewMap(sb, types.SessionsKey, "sessions", collections.BytesKey, codec.CollValue[types.SigningSession](cdc)),
		ActiveSessions: collections.NewKeySet(sb, types.ActiveSessionsKey, "active_sessions", collections.BytesKey),
		Commitments: collections.NewMap(sb, types.CommitmentsKey, "commitments",
			collections.TripleKeyCodec(collections.Uint64Key, collections.Uint32Key, collections.Uint64Key), codec.CollValue[types.StoredCommitment](cdc)),
		PoolHead: collections.NewMap(sb, types.PoolHeadKey, "pool_head",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint32Key), collections.Uint64Value),
		SignerStats: collections.NewMap(sb, types.SignerStatsKey, "signer_stats",
			collections.PairKeyCodec(collections.Uint64Key, sdk.AccAddressKey), codec.CollValue[types.SignerStats](cdc)),
		CustodyKeys:   collections.NewMap(sb, types.CustodyKeysKey, "custody_keys", sdk.AccAddressKey, codec.CollValue[types.CustodyKey](cdc)),
		HotKeys:       collections.NewMap(sb, types.HotKeysKey, "hot_keys", sdk.AccAddressKey, collections.BytesValue),
		DKGStates:     collections.NewMap(sb, types.DKGStatesKey, "dkg_states", collections.Uint64Key, codec.CollValue[types.DKGState](cdc)),
		Pending:       collections.NewItem(sb, types.PendingKey, "pending_membership", codec.CollValue[types.PendingMembership](cdc)),
		SlashedEpochs: collections.NewMap(sb, types.SlashedEpochsKey, "slashed_epochs", collections.Uint64Key, collections.Int64Value),
		TheftPaused:   collections.NewItem(sb, types.TheftPausedKey, "theft_paused", collections.BoolValue),
		DKGMessages: collections.NewMap(sb, types.DKGMessagesKey, "dkg_messages",
			collections.TripleKeyCodec(collections.Uint64Key, collections.Uint32Key, collections.BytesKey), collections.BytesValue),
		admin: admin,
		vals:  vals,
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// SetVaultKeeper attaches x/vault (used to credit and debit deposits).
func (k *Keeper) SetVaultKeeper(v types.VaultKeeper) { k.vault = v }

func checkSat(sat sdkmath.Int) error {
	if sat.IsNil() || sat.IsNegative() {
		return errorsmod.Wrapf(types.ErrInvalidAmount, "%v", sat)
	}
	return nil
}

// GetParams returns the current params.
func (k *Keeper) GetParams(ctx context.Context) (types.Params, error) { return k.Params.Get(ctx) }

// GetClaimable returns an account's claimable satoshis (zero if none).
func (k *Keeper) GetClaimable(ctx context.Context, addr sdk.AccAddress) (sdkmath.Int, error) {
	v, err := k.Claimable.Get(ctx, addr)
	if errors.Is(err, collections.ErrNotFound) {
		return sdkmath.ZeroInt(), nil
	}
	return v, err
}

// CreditClaimable adds sat to an account's claimable balance.
func (k *Keeper) CreditClaimable(ctx context.Context, addr sdk.AccAddress, sat sdkmath.Int) error {
	if err := checkSat(sat); err != nil || sat.IsZero() {
		return err
	}
	cur, err := k.GetClaimable(ctx, addr)
	if err != nil {
		return err
	}
	return k.Claimable.Set(ctx, addr, cur.Add(sat))
}

// DebitClaimable removes sat from an account's claimable balance.
func (k *Keeper) DebitClaimable(ctx context.Context, addr sdk.AccAddress, sat sdkmath.Int) error {
	if err := checkSat(sat); err != nil || sat.IsZero() {
		return err
	}
	cur, err := k.GetClaimable(ctx, addr)
	if err != nil {
		return err
	}
	if cur.LT(sat) {
		return errorsmod.Wrapf(types.ErrInsufficient, "%s has %s sat, needs %s", addr, cur, sat)
	}
	if cur.Equal(sat) {
		return k.Claimable.Remove(ctx, addr)
	}
	return k.Claimable.Set(ctx, addr, cur.Sub(sat))
}

// TotalClaimable sums every claimable balance.
func (k *Keeper) TotalClaimable(ctx context.Context) (sdkmath.Int, error) {
	total := sdkmath.ZeroInt()
	err := k.Claimable.Walk(ctx, nil, func(_ sdk.AccAddress, v sdkmath.Int) (bool, error) {
		total = total.Add(v)
		return false, nil
	})
	return total, err
}

// GetCredited returns the satoshis credited as vault collateral.
func (k *Keeper) GetCredited(ctx context.Context) (sdkmath.Int, error) { return k.Credited.Get(ctx) }

// GetFeeReserve returns the fee reserve in satoshis.
func (k *Keeper) GetFeeReserve(ctx context.Context) (sdkmath.Int, error) {
	return k.FeeReserve.Get(ctx)
}

// GetDeficit returns the custody deficit in satoshis (design §9.7 step 6).
func (k *Keeper) GetDeficit(ctx context.Context) (sdkmath.Int, error) { return k.Deficit.Get(ctx) }

// CheckNoDeficit fails while the custody deficit is non-zero: new withdrawal requests
// are rejected until it is cleared (spec M3-T6).
func (k *Keeper) CheckNoDeficit(ctx context.Context) error {
	d, err := k.Deficit.Get(ctx)
	if err != nil {
		return err
	}
	if d.IsPositive() {
		return errorsmod.Wrapf(types.ErrDeficit, "custody deficit is %s sat", d)
	}
	return nil
}

// CapHeadroom is max(0, TreasuryCapSat − credited).
func (k *Keeper) CapHeadroom(ctx context.Context) (sdkmath.Int, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	c, err := k.Credited.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	h := p.TreasuryCapSat.Sub(c)
	if h.IsNegative() {
		return sdkmath.ZeroInt(), nil
	}
	return h, nil
}

// AddCredited records sat newly credited as collateral. It fails if sat exceeds the
// cap headroom, so credited never passes the cap at the moment of crediting
// (invariant 14).
func (k *Keeper) AddCredited(ctx context.Context, sat sdkmath.Int) error {
	if err := checkSat(sat); err != nil || sat.IsZero() {
		return err
	}
	h, err := k.CapHeadroom(ctx)
	if err != nil {
		return err
	}
	if sat.GT(h) {
		return errorsmod.Wrapf(types.ErrOverCap, "credit %s sat, headroom %s sat", sat, h)
	}
	c, err := k.Credited.Get(ctx)
	if err != nil {
		return err
	}
	return k.Credited.Set(ctx, c.Add(sat))
}

// SubCredited records sat leaving vault collateral (withdrawal, seizure, auction, reorg).
func (k *Keeper) SubCredited(ctx context.Context, sat sdkmath.Int) error {
	if err := checkSat(sat); err != nil || sat.IsZero() {
		return err
	}
	c, err := k.Credited.Get(ctx)
	if err != nil {
		return err
	}
	if c.LT(sat) {
		return errorsmod.Wrapf(types.ErrUnderflow, "credited %s, sub %s", c, sat)
	}
	return k.Credited.Set(ctx, c.Sub(sat))
}

// UpdateParams applies MsgUpdateParams: the signer must be the x/poa admin.
func (k *Keeper) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.admin.CheckAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	return k.Params.Set(ctx, msg.Params)
}

// InitGenesis stores the params, any genesis key epochs (localnet chain ids only) and
// the custody keys; the counters start at zero.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := gs.CheckChainID(sdk.UnwrapSDKContext(ctx).ChainID()); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	for _, item := range []collections.Item[sdkmath.Int]{k.Credited, k.FeeReserve, k.Deficit} {
		if err := item.Set(ctx, sdkmath.ZeroInt()); err != nil {
			return err
		}
	}
	for _, e := range gs.Epochs {
		if err := k.Epochs.Set(ctx, e.Number, e); err != nil {
			return err
		}
	}
	if err := k.ActiveEpoch.Set(ctx, gs.ActiveEpoch); err != nil {
		return err
	}
	for _, s := range gs.ShareIndices {
		if err := k.ShareIndex.Set(ctx, collections.Join(s.Epoch, sdk.MustAccAddressFromBech32(s.Operator)), s.Index); err != nil {
			return err
		}
	}
	for _, v := range gs.VerifyingShares {
		if err := k.VerifyingShares.Set(ctx, collections.Join(v.Epoch, v.Index), v.Pubkey); err != nil {
			return err
		}
	}
	for _, ck := range gs.CustodyKeys {
		if err := k.setCustodyKey(ctx, ck); err != nil {
			return err
		}
	}
	return nil
}

// ExportGenesis returns the params and the key epochs with their shares.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	gs := &types.GenesisState{Params: p}
	if gs.ActiveEpoch, err = k.ActiveEpoch.Get(ctx); err != nil {
		return nil, err
	}
	err = k.Epochs.Walk(ctx, nil, func(_ uint64, e types.KeyEpoch) (bool, error) {
		gs.Epochs = append(gs.Epochs, e)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	err = k.VerifyingShares.Walk(ctx, nil, func(key collections.Pair[uint64, uint32], y []byte) (bool, error) {
		gs.VerifyingShares = append(gs.VerifyingShares, types.VerifyingShare{Epoch: key.K1(), Index: key.K2(), Pubkey: y})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	err = k.ShareIndex.Walk(ctx, nil, func(key collections.Pair[uint64, sdk.AccAddress], idx uint32) (bool, error) {
		gs.ShareIndices = append(gs.ShareIndices, types.ShareIndex{Epoch: key.K1(), Operator: key.K2().String(), Index: idx})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	sortShareIndices(gs.ShareIndices)
	err = k.CustodyKeys.Walk(ctx, nil, func(_ sdk.AccAddress, ck types.CustodyKey) (bool, error) {
		gs.CustodyKeys = append(gs.CustodyKeys, ck)
		return false, nil
	})
	return gs, err
}
