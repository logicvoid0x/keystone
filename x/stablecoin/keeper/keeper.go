package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/OWNER/keystone/x/stablecoin/types"
)

// Keeper is the KUSD mint/burn authority, the surplus buffer and bad debt (design §7.4).
type Keeper struct {
	Schema       collections.Schema
	Params       collections.Item[types.Params]
	Sin          collections.Item[sdkmath.Int]
	SinOverSince collections.Item[int64]
	MintsPaused  collections.Item[bool]

	admin types.AdminKeeper
	bank  types.BankKeeper
}

// NewKeeper builds the keeper. admin authorizes MsgUpdateParams (x/poa).
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, admin types.AdminKeeper, bank types.BankKeeper) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params:       collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Sin:          collections.NewItem(sb, types.SinKey, "sin", sdk.IntValue),
		SinOverSince: collections.NewItem(sb, types.SinOverSinceKey, "sin_over_since", collections.Int64Value),
		MintsPaused:  collections.NewItem(sb, types.MintsPausedKey, "mints_paused", collections.BoolValue),
		admin:        admin,
		bank:         bank,
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

func kusd(amt sdkmath.Int) sdk.Coins { return sdk.NewCoins(sdk.NewCoin(types.Denom, amt)) }

func checkAmount(amt sdkmath.Int) error {
	if amt.IsNil() || amt.IsNegative() {
		return errorsmod.Wrapf(types.ErrInvalidAmount, "%v", amt)
	}
	return nil
}

// GetParams returns the current params.
func (k *Keeper) GetParams(ctx context.Context) (types.Params, error) { return k.Params.Get(ctx) }

// Mint mints amt KUSD to an account.
func (k *Keeper) Mint(ctx context.Context, to sdk.AccAddress, amt sdkmath.Int) error {
	if err := checkAmount(amt); err != nil || amt.IsZero() {
		return err
	}
	if err := k.bank.MintCoins(ctx, types.ModuleName, kusd(amt)); err != nil {
		return err
	}
	return k.bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, to, kusd(amt))
}

// BurnFrom moves amt KUSD from an account to this module and burns it (repay).
func (k *Keeper) BurnFrom(ctx context.Context, from sdk.AccAddress, amt sdkmath.Int) error {
	if err := checkAmount(amt); err != nil || amt.IsZero() {
		return err
	}
	if err := k.bank.SendCoinsFromAccountToModule(ctx, from, types.ModuleName, kusd(amt)); err != nil {
		return err
	}
	return k.bank.BurnCoins(ctx, types.ModuleName, kusd(amt))
}

// BurnFromModule burns amt KUSD held by a module account with Burner permission.
func (k *Keeper) BurnFromModule(ctx context.Context, module string, amt sdkmath.Int) error {
	if err := checkAmount(amt); err != nil || amt.IsZero() {
		return err
	}
	return k.bank.BurnCoins(ctx, module, kusd(amt))
}

// AccrueSurplus mints amt KUSD of stability-fee revenue. While sin is zero,
// ⌊amt × StakerShareBps / 10,000⌋ goes to the fee collector for x/distribution to pay
// stakers (design §7.7, M7); the rest goes to the surplus buffer.
func (k *Keeper) AccrueSurplus(ctx context.Context, amt sdkmath.Int) error {
	if err := checkAmount(amt); err != nil || amt.IsZero() {
		return err
	}
	share, err := k.stakerShare(ctx, amt)
	if err != nil {
		return err
	}
	if err := k.bank.MintCoins(ctx, types.ModuleName, kusd(amt)); err != nil {
		return err
	}
	if err := k.bank.SendCoinsFromModuleToModule(ctx, types.ModuleName, types.SurplusModuleAccount, kusd(amt.Sub(share))); err != nil {
		return err
	}
	if share.IsZero() {
		return nil
	}
	if err := k.bank.SendCoinsFromModuleToModule(ctx, types.ModuleName, types.FeeCollector, kusd(share)); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeStakerShare,
		sdk.NewAttribute(types.AttributeKeyAmount, share.String())))
	return nil
}

// stakerShare is ⌊amt × StakerShareBps / 10,000⌋, or zero while sin > 0.
func (k *Keeper) stakerShare(ctx context.Context, amt sdkmath.Int) (sdkmath.Int, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	if p.StakerShareBps == 0 {
		return sdkmath.ZeroInt(), nil
	}
	sin, err := k.Sin.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	if sin.IsPositive() {
		return sdkmath.ZeroInt(), nil
	}
	return amt.MulRaw(int64(p.StakerShareBps)).QuoRaw(10_000), nil
}

// AddToSurplus moves amt KUSD already held by a module into the surplus buffer
// (auction proceeds above the auction's bad debt).
func (k *Keeper) AddToSurplus(ctx context.Context, fromModule string, amt sdkmath.Int) error {
	if err := checkAmount(amt); err != nil || amt.IsZero() {
		return err
	}
	return k.bank.SendCoinsFromModuleToModule(ctx, fromModule, types.SurplusModuleAccount, kusd(amt))
}

// RecordBadDebt adds amt to sin and emits bad_debt{amount, source}.
func (k *Keeper) RecordBadDebt(ctx context.Context, amt sdkmath.Int, source string) error {
	if err := checkAmount(amt); err != nil || amt.IsZero() {
		return err
	}
	sin, err := k.Sin.Get(ctx)
	if err != nil {
		return err
	}
	if err := k.Sin.Set(ctx, sin.Add(amt)); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeBadDebt,
		sdk.NewAttribute(types.AttributeKeyAmount, amt.String()),
		sdk.NewAttribute(types.AttributeKeySource, source)))
	return nil
}

// BurnAgainstSin burns amt KUSD held by a module and reduces sin by amt. amt must
// not exceed sin.
func (k *Keeper) BurnAgainstSin(ctx context.Context, module string, amt sdkmath.Int) error {
	if err := checkAmount(amt); err != nil || amt.IsZero() {
		return err
	}
	sin, err := k.Sin.Get(ctx)
	if err != nil {
		return err
	}
	if amt.GT(sin) {
		return errorsmod.Wrapf(types.ErrInvalidAmount, "burn %s exceeds sin %s", amt, sin)
	}
	if err := k.bank.BurnCoins(ctx, module, kusd(amt)); err != nil {
		return err
	}
	return k.Sin.Set(ctx, sin.Sub(amt))
}

// CoverShortfall moves up to amt KUSD from surplus to module `to` and returns the
// amount moved (pool mode step 3, design §8.4).
func (k *Keeper) CoverShortfall(ctx context.Context, to string, amt sdkmath.Int) (sdkmath.Int, error) {
	if err := checkAmount(amt); err != nil {
		return sdkmath.Int{}, err
	}
	covered := sdkmath.MinInt(amt, k.SurplusBalance(ctx))
	if covered.IsZero() {
		return covered, nil
	}
	if err := k.bank.SendCoinsFromModuleToModule(ctx, types.SurplusModuleAccount, to, kusd(covered)); err != nil {
		return sdkmath.Int{}, err
	}
	return covered, nil
}

// SurplusBalance is the surplus module account's KUSD balance.
func (k *Keeper) SurplusBalance(ctx context.Context) sdkmath.Int {
	return k.bank.GetBalance(ctx, authtypes.NewModuleAddress(types.SurplusModuleAccount), types.Denom).Amount
}

// Supply is the total KUSD supply.
func (k *Keeper) Supply(ctx context.Context) sdkmath.Int {
	return k.bank.GetSupply(ctx, types.Denom).Amount
}

// GetSin returns the bad debt.
func (k *Keeper) GetSin(ctx context.Context) (sdkmath.Int, error) { return k.Sin.Get(ctx) }

// AreMintsPaused reports the bad-debt mint pause (design §7.4).
func (k *Keeper) AreMintsPaused(ctx context.Context) (bool, error) { return k.MintsPaused.Get(ctx) }

// EndBlocker heals bad debt from surplus, then updates the mint pause.
func (k *Keeper) EndBlocker(ctx context.Context) error {
	if err := k.Heal(ctx); err != nil {
		return err
	}
	return k.updatePause(ctx)
}

// Heal burns min(surplus, sin) from surplus and reduces sin by the same amount.
func (k *Keeper) Heal(ctx context.Context) error {
	sin, err := k.Sin.Get(ctx)
	if err != nil {
		return err
	}
	amt := sdkmath.MinInt(sin, k.SurplusBalance(ctx))
	if !amt.IsPositive() {
		return nil
	}
	if err := k.bank.SendCoinsFromModuleToModule(ctx, types.SurplusModuleAccount, types.ModuleName, kusd(amt)); err != nil {
		return err
	}
	if err := k.bank.BurnCoins(ctx, types.ModuleName, kusd(amt)); err != nil {
		return err
	}
	if err := k.Sin.Set(ctx, sin.Sub(amt)); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeHealed,
		sdk.NewAttribute(types.AttributeKeyAmount, amt.String())))
	return nil
}

// updatePause pauses mints once sin has stayed above SinPauseBps of supply for
// SinPauseBlocks blocks, and unpauses when it falls back to or below the threshold.
func (k *Keeper) updatePause(ctx context.Context) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	sin, err := k.Sin.Get(ctx)
	if err != nil {
		return err
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	over := sin.IsPositive() && sin.MulRaw(10_000).GT(k.Supply(ctx).MulRaw(int64(p.SinPauseBps)))
	paused, err := k.MintsPaused.Get(ctx)
	if err != nil {
		return err
	}
	if !over {
		if err := k.SinOverSince.Set(ctx, 0); err != nil {
			return err
		}
		return k.MintsPaused.Set(ctx, false)
	}
	since, err := k.SinOverSince.Get(ctx)
	if err != nil {
		return err
	}
	if since == 0 {
		since = height
		if err := k.SinOverSince.Set(ctx, since); err != nil {
			return err
		}
	}
	if !paused && height-since >= int64(p.SinPauseBlocks) {
		if err := k.MintsPaused.Set(ctx, true); err != nil {
			return err
		}
		sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeMintsPaused,
			sdk.NewAttribute(types.AttributeKeySin, sin.String())))
	}
	return nil
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

// InitGenesis stores the params; sin starts at zero, mints unpaused (design §10.4).
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	if err := k.Sin.Set(ctx, sdkmath.ZeroInt()); err != nil {
		return err
	}
	if err := k.SinOverSince.Set(ctx, 0); err != nil {
		return err
	}
	return k.MintsPaused.Set(ctx, false)
}

// ExportGenesis returns the params.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p}, nil
}
