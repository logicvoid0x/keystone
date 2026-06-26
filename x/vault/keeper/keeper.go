package keeper

import (
	"context"
	"errors"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/vault/types"
)

// Keeper owns vaults, the rate index and the liquidation index (design §7, §8.1).
type Keeper struct {
	Schema        collections.Schema
	Params        collections.Item[types.Params]
	Vaults        collections.Map[uint64, types.Vault]
	NextVaultID   collections.Sequence
	OwnerIndex    collections.KeySet[collections.Pair[sdk.AccAddress, uint64]]
	LiqIndex      collections.Map[collections.Pair[[]byte, uint64], bool]
	RateState     collections.Item[types.RateState]
	TotalArt      collections.Item[sdkmath.Int]
	CeilingMark   collections.Item[types.CeilingMark]
	RoundingSlack collections.Item[sdkmath.Int]

	admin      types.AdminKeeper
	oracle     types.OracleKeeper
	stablecoin types.StablecoinKeeper
	custody    types.CustodyKeeper
}

// NewKeeper builds the keeper.
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, admin types.AdminKeeper,
	oracle types.OracleKeeper, stablecoin types.StablecoinKeeper, custody types.CustodyKeeper,
) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params:      collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Vaults:      collections.NewMap(sb, types.VaultsKey, "vaults", collections.Uint64Key, codec.CollValue[types.Vault](cdc)),
		NextVaultID: collections.NewSequence(sb, types.NextVaultIDKey, "next_vault_id"),
		OwnerIndex: collections.NewKeySet(sb, types.OwnerIndexKey, "owner_index",
			collections.PairKeyCodec(sdk.AccAddressKey, collections.Uint64Key)),
		LiqIndex: collections.NewMap(sb, types.LiqIndexKey, "liq_index",
			collections.PairKeyCodec(collections.BytesKey, collections.Uint64Key), collections.BoolValue),
		RateState:     collections.NewItem(sb, types.RateStateKey, "rate_state", codec.CollValue[types.RateState](cdc)),
		TotalArt:      collections.NewItem(sb, types.TotalArtKey, "total_art", sdk.IntValue),
		CeilingMark:   collections.NewItem(sb, types.CeilingMarkKey, "ceiling_mark", codec.CollValue[types.CeilingMark](cdc)),
		RoundingSlack: collections.NewItem(sb, types.RoundingSlackKey, "rounding_slack", sdk.IntValue),
		admin:         admin,
		oracle:        oracle,
		stablecoin:    stablecoin,
		custody:       custody,
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// GetParams returns the current params.
func (k *Keeper) GetParams(ctx context.Context) (types.Params, error) { return k.Params.Get(ctx) }

// GetVault returns a vault.
func (k *Keeper) GetVault(ctx context.Context, id uint64) (types.Vault, error) {
	v, err := k.Vaults.Get(ctx, id)
	if errors.Is(err, collections.ErrNotFound) {
		return v, errorsmod.Wrapf(types.ErrVaultNotFound, "%d", id)
	}
	return v, err
}

// Rate returns the current rate (RAY).
func (k *Keeper) Rate(ctx context.Context) (sdkmath.Int, error) {
	rs, err := k.RateState.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	return rs.Rate, nil
}

// Debt is ceil(art × rate / RAY) for a vault.
func (k *Keeper) Debt(ctx context.Context, v types.Vault) (sdkmath.Int, error) {
	r, err := k.Rate(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	return types.DebtOf(v.Art, r), nil
}

// TotalDebt is ceil(totalArt × rate / RAY), the aggregate used by the ceiling check.
func (k *Keeper) TotalDebt(ctx context.Context) (sdkmath.Int, error) {
	r, err := k.Rate(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	a, err := k.TotalArt.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	return types.DebtOf(a, r), nil
}

// setVault writes a vault, rewriting its liquidation-index entry from oldKey (design
// §8.1: rewritten on every change to art or collateral; art = 0 is not indexed).
func (k *Keeper) setVault(ctx context.Context, prev *types.Vault, v types.Vault) error {
	if prev != nil && prev.Art.IsPositive() {
		if err := k.LiqIndex.Remove(ctx, collections.Join(types.LiqKey(prev.Art, prev.CollateralSat), prev.Id)); err != nil {
			return err
		}
	}
	if v.Art.IsPositive() {
		if err := k.LiqIndex.Set(ctx, collections.Join(types.LiqKey(v.Art, v.CollateralSat), v.Id), true); err != nil {
			return err
		}
	}
	return k.Vaults.Set(ctx, v.Id, v)
}

func (k *Keeper) addTotalArt(ctx context.Context, delta sdkmath.Int) error {
	a, err := k.TotalArt.Get(ctx)
	if err != nil {
		return err
	}
	return k.TotalArt.Set(ctx, a.Add(delta))
}

// addSlack raises the invariant-1 rounding bound (docs/decisions.md).
func (k *Keeper) addSlack(ctx context.Context, n sdkmath.Int) error {
	s, err := k.RoundingSlack.Get(ctx)
	if err != nil {
		return err
	}
	return k.RoundingSlack.Set(ctx, s.Add(n))
}

// opSlack is the most one partial mint or repay can add to Σdebt + sin − supply:
// ceil(rate / RAY).
func opSlack(rate sdkmath.Int) sdkmath.Int {
	q := rate.Quo(types.RAY)
	if !rate.Mod(types.RAY).IsZero() {
		q = q.AddRaw(1)
	}
	return q
}

// Drip accrues the stability fee up to blockTime (design §7.3): rate = floor(rate ×
// rpow(fee, Δt) / RAY), and floor(totalArt × Δrate / RAY) KUSD is minted to surplus.
func (k *Keeper) Drip(ctx context.Context, blockTime time.Time) error {
	rs, err := k.RateState.Get(ctx)
	if err != nil {
		return err
	}
	now := blockTime.Unix()
	if now <= rs.LastDripUnix {
		return nil
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	dt := uint64(now - rs.LastDripUnix)
	newRate := rs.Rate.Mul(types.Rpow(p.StabilityFeePerSecond, dt, types.RAY)).Quo(types.RAY)
	totalArt, err := k.TotalArt.Get(ctx)
	if err != nil {
		return err
	}
	fee := totalArt.Mul(newRate.Sub(rs.Rate)).Quo(types.RAY)
	if fee.IsPositive() {
		if err := k.stablecoin.AccrueSurplus(ctx, fee); err != nil {
			return err
		}
	}
	if totalArt.IsPositive() {
		if err := k.addSlack(ctx, sdkmath.OneInt()); err != nil {
			return err
		}
	}
	return k.RateState.Set(ctx, types.RateState{Rate: newRate, LastDripUnix: now})
}

// IterateLiquidatable walks the liquidation index worst-first and calls fn for each
// vault liquidatable at price, stopping at the first vault that fails the exact §8.1
// test (that stop rule is part of the definition, design §8.2).
func (k *Keeper) IterateLiquidatable(ctx context.Context, price sdkmath.Int, fn func(types.Vault) (stop bool)) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	rate, err := k.Rate(ctx)
	if err != nil {
		return err
	}
	iter, err := k.LiqIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		v, err := k.Vaults.Get(ctx, key.K2())
		if err != nil {
			return err
		}
		if !types.IsLiquidatable(v.CollateralSat, types.DebtOf(v.Art, rate), price, p.LrBps) {
			return nil
		}
		if fn(v) {
			return nil
		}
	}
	return nil
}

// IsLiquidatable applies the §8.1 test to one vault at price.
func (k *Keeper) IsLiquidatable(ctx context.Context, v types.Vault, price sdkmath.Int) (bool, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return false, err
	}
	debt, err := k.Debt(ctx, v)
	if err != nil {
		return false, err
	}
	return types.IsLiquidatable(v.CollateralSat, debt, price, p.LrBps), nil
}

// AnyLiquidatable reports whether the worst vault is liquidatable at price.
func (k *Keeper) AnyLiquidatable(ctx context.Context, price sdkmath.Int) (bool, error) {
	found := false
	err := k.IterateLiquidatable(ctx, price, func(types.Vault) bool { found = true; return true })
	return found, err
}

// CloseForLiquidation removes a vault's whole debt and seizeSat of its collateral,
// and returns the debt (design §8.4). Seized satoshis leave credited collateral.
func (k *Keeper) CloseForLiquidation(ctx context.Context, id uint64, seizeSat sdkmath.Int) (sdkmath.Int, error) {
	v, err := k.GetVault(ctx, id)
	if err != nil {
		return sdkmath.Int{}, err
	}
	if seizeSat.IsNegative() || seizeSat.GT(v.CollateralSat) {
		return sdkmath.Int{}, errorsmod.Wrapf(types.ErrInsufficient, "seize %s of %s", seizeSat, v.CollateralSat)
	}
	debt, err := k.Debt(ctx, v)
	if err != nil {
		return sdkmath.Int{}, err
	}
	prev := v
	if err := k.addTotalArt(ctx, v.Art.Neg()); err != nil {
		return sdkmath.Int{}, err
	}
	v.Art = sdkmath.ZeroInt()
	v.CollateralSat = v.CollateralSat.Sub(seizeSat)
	v.Status = types.VAULT_STATUS_LIQUIDATED
	if err := k.setVault(ctx, &prev, v); err != nil {
		return sdkmath.Int{}, err
	}
	if err := k.custody.SubCredited(ctx, seizeSat); err != nil {
		return sdkmath.Int{}, err
	}
	if err := k.addSlack(ctx, sdkmath.OneInt()); err != nil {
		return sdkmath.Int{}, err
	}
	return debt, nil
}

// CreditCollateral adds sat to a vault within the treasury-cap headroom; any excess
// goes to the owner's claimable balance (design §3.2). It returns the satoshis
// credited to the vault.
func (k *Keeper) CreditCollateral(ctx context.Context, id uint64, sat sdkmath.Int) (sdkmath.Int, error) {
	if sat.IsNil() || !sat.IsPositive() {
		return sdkmath.Int{}, errorsmod.Wrap(types.ErrInvalidAmount, "sat must be positive")
	}
	v, err := k.GetVault(ctx, id)
	if err != nil {
		return sdkmath.Int{}, err
	}
	if v.Status == types.VAULT_STATUS_CLOSED {
		return sdkmath.Int{}, errorsmod.Wrapf(types.ErrVaultStatus, "vault %d is closed", id)
	}
	headroom, err := k.custody.CapHeadroom(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	credit := sdkmath.MinInt(sat, headroom)
	over := sat.Sub(credit)
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if credit.IsPositive() {
		if err := k.custody.AddCredited(ctx, credit); err != nil {
			return sdkmath.Int{}, err
		}
		prev := v
		v.CollateralSat = v.CollateralSat.Add(credit)
		v.Status = types.VAULT_STATUS_ACTIVE
		if err := k.setVault(ctx, &prev, v); err != nil {
			return sdkmath.Int{}, err
		}
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeCollateralCredited,
			sdk.NewAttribute(types.AttributeKeyID, uintStr(id)),
			sdk.NewAttribute(types.AttributeKeySat, credit.String())))
	}
	if over.IsPositive() {
		owner, err := sdk.AccAddressFromBech32(v.Owner)
		if err != nil {
			return sdkmath.Int{}, err
		}
		if err := k.custody.CreditClaimable(ctx, owner, over); err != nil {
			return sdkmath.Int{}, err
		}
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeDepositOverCap,
			sdk.NewAttribute(types.AttributeKeyID, uintStr(id)),
			sdk.NewAttribute(types.AttributeKeySat, over.String())))
	}
	return credit, nil
}

// DebitCollateral removes sat of collateral (reorg path, M3). Collateral cannot go
// below zero; the shortfall is returned for the custody deficit.
func (k *Keeper) DebitCollateral(ctx context.Context, id uint64, sat sdkmath.Int) (shortfall sdkmath.Int, err error) {
	v, err := k.GetVault(ctx, id)
	if err != nil {
		return sdkmath.Int{}, err
	}
	take := sdkmath.MinInt(sat, v.CollateralSat)
	prev := v
	v.CollateralSat = v.CollateralSat.Sub(take)
	if err := k.setVault(ctx, &prev, v); err != nil {
		return sdkmath.Int{}, err
	}
	if err := k.custody.SubCredited(ctx, take); err != nil {
		return sdkmath.Int{}, err
	}
	return sat.Sub(take), nil
}

// InitGenesis stores params; rate = 1 RAY, last drip = genesis time, no vaults
// (design §10.4).
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	t := sdk.UnwrapSDKContext(ctx).BlockTime().Unix()
	if err := k.RateState.Set(ctx, types.RateState{Rate: types.RAY, LastDripUnix: t}); err != nil {
		return err
	}
	if err := k.TotalArt.Set(ctx, sdkmath.ZeroInt()); err != nil {
		return err
	}
	if err := k.CeilingMark.Set(ctx, types.CeilingMark{Rate: types.RAY, Ceiling: gs.Params.GlobalDebtCeiling}); err != nil {
		return err
	}
	return k.RoundingSlack.Set(ctx, sdkmath.ZeroInt())
}

// ExportGenesis returns the params.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p}, nil
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

// VaultOwner returns a vault's owner and whether it can still take collateral (not
// closed). Used by x/custody to credit deposits.
func (k *Keeper) VaultOwner(ctx context.Context, id uint64) (sdk.AccAddress, bool, error) {
	v, err := k.GetVault(ctx, id)
	if err != nil {
		return nil, false, err
	}
	owner, err := sdk.AccAddressFromBech32(v.Owner)
	if err != nil {
		return nil, false, err
	}
	return owner, v.Status != types.VAULT_STATUS_CLOSED, nil
}
