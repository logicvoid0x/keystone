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

	"github.com/OWNER/keystone/x/custody/types"
)

// Keeper is the M1 custody stub: claimable balances and the treasury-cap counter.
// Credited = satoshis currently credited as vault collateral (docs/decisions.md).
// Key epochs, deposits, UTXOs and withdrawals arrive in M3.
type Keeper struct {
	Schema    collections.Schema
	Params    collections.Item[types.Params]
	Claimable collections.Map[sdk.AccAddress, sdkmath.Int]
	Credited  collections.Item[sdkmath.Int]

	admin types.AdminKeeper
}

// NewKeeper builds the keeper. admin authorizes MsgUpdateParams (x/poa).
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, admin types.AdminKeeper) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params:    collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Claimable: collections.NewMap(sb, types.ClaimableKey, "claimable", sdk.AccAddressKey, sdk.IntValue),
		Credited:  collections.NewItem(sb, types.CreditedKey, "credited", sdk.IntValue),
		admin:     admin,
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

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

// SubCredited records sat leaving vault collateral (withdrawal, seizure, auction).
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

// CustodiedSat is the satoshis Keystone accounts for: credited collateral plus
// claimable balances. (M3 replaces this with the UTXO sum.)
func (k *Keeper) CustodiedSat(ctx context.Context) (sdkmath.Int, error) {
	c, err := k.Credited.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	cl, err := k.TotalClaimable(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	return c.Add(cl), nil
}

// ApplyOps applies the custody batch. No-op in M1 (design §5.9 step order is kept).
func (k *Keeper) ApplyOps(context.Context) error { return nil }

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

// InitGenesis stores the params; credited starts at zero.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	return k.Credited.Set(ctx, sdkmath.ZeroInt())
}

// ExportGenesis returns the params.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p}, nil
}
