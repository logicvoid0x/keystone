package keeper

import (
	"context"
	"errors"
	"slices"
	"strings"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/logicvoid0x/keystone/x/admin/types"
)

// Keeper owns the admin address and the validator allowlist (design §4.2).
type Keeper struct {
	Schema    collections.Schema
	Params    collections.Item[types.Params]
	Allowlist collections.Map[sdk.AccAddress, types.AllowEntry]

	staking types.StakingKeeper
	guard   types.RemovalGuard
	router  baseapp.MessageRouter
	// execTypes are the message type URLs MsgExec accepts, sorted.
	execTypes []string
}

// NewKeeper builds the keeper with the AllowAll removal guard and no MsgExec types.
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService, sk types.StakingKeeper) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params:    collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Allowlist: collections.NewMap(sb, types.AllowlistKey, "allowlist", sdk.AccAddressKey, codec.CollValue[types.AllowEntry](cdc)),
		staking:   sk,
		guard:     types.AllowAll{},
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// SetRemovalGuard replaces the removal guard (x/custody installs its guard).
func (k *Keeper) SetRemovalGuard(g types.RemovalGuard) { k.guard = g }

// SetExecRouter sets the router MsgExec dispatches to and the message type URLs it
// accepts. Every accepted type must carry an authority field (GetAuthority).
func (k *Keeper) SetExecRouter(r baseapp.MessageRouter, typeURLs []string) {
	k.router = r
	k.execTypes = slices.Sorted(slices.Values(typeURLs))
}

// ModuleAddress is the x/admin module account: the authority of the SDK keepers,
// which only MsgExec (or the upgrade and consensus wrappers) can act for.
func ModuleAddress() sdk.AccAddress { return authtypes.NewModuleAddress(types.ModuleName) }

// Admin returns the admin address from params.
func (k *Keeper) Admin(ctx context.Context) (sdk.AccAddress, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return sdk.AccAddressFromBech32(p.Admin)
}

// IsAllowlisted reports whether operator (account bytes = valoper bytes) may run a
// validator.
func (k *Keeper) IsAllowlisted(ctx context.Context, operator sdk.AccAddress) (bool, error) {
	return k.Allowlist.Has(ctx, operator)
}

// AllEntries returns the allowlist sorted by operator address bytes.
func (k *Keeper) AllEntries(ctx context.Context) ([]types.AllowEntry, error) {
	var out []types.AllowEntry
	err := k.Allowlist.Walk(ctx, nil, func(_ sdk.AccAddress, e types.AllowEntry) (bool, error) {
		out = append(out, e)
		return false, nil
	})
	return out, err
}

// SetAllowlist applies MsgSetAllowlist. Additions must be new; removals must be
// listed, must leave the allowlist non-empty and must pass the removal guard. Each
// removed operator whose validator exists and is not jailed is jailed.
func (k *Keeper) SetAllowlist(ctx context.Context, msg *types.MsgSetAllowlist) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.checkAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	removed, err := types.ParseSorted(msg.Remove)
	if err != nil {
		return err
	}
	for _, r := range removed {
		if has, err := k.Allowlist.Has(ctx, r); err != nil {
			return err
		} else if !has {
			return errorsmod.Wrapf(types.ErrNotAllowlisted, "%s", r)
		}
	}
	if len(removed) > 0 {
		if err := k.guard.AllowRemoval(ctx, removed); err != nil {
			return errorsmod.Wrap(types.ErrGuardRefused, err.Error())
		}
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	added := make([]string, 0, len(msg.Add))
	for _, in := range msg.Add {
		acc := sdk.MustAccAddressFromBech32(in.Operator)
		if has, err := k.Allowlist.Has(ctx, acc); err != nil {
			return err
		} else if has {
			return errorsmod.Wrapf(types.ErrAlreadyListed, "%s", acc)
		}
		if err := k.Allowlist.Set(ctx, acc, types.AllowEntry{Operator: acc.String(), Moniker: in.Moniker, AddedHeight: height}); err != nil {
			return err
		}
		added = append(added, acc.String())
	}
	for _, r := range removed {
		if err := k.Allowlist.Remove(ctx, r); err != nil {
			return err
		}
	}
	if empty, err := k.isEmpty(ctx); err != nil {
		return err
	} else if empty {
		return types.ErrEmptyAllowlist
	}
	for _, r := range removed {
		if err := k.jailIfBonded(ctx, r); err != nil {
			return err
		}
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeAllowlistUpdated,
		sdk.NewAttribute(types.AttributeKeyAdded, strings.Join(added, ",")),
		sdk.NewAttribute(types.AttributeKeyRemoved, strings.Join(msg.Remove, ",")),
	))
	return nil
}

func (k *Keeper) isEmpty(ctx context.Context) (bool, error) {
	empty := true
	err := k.Allowlist.Walk(ctx, nil, func(sdk.AccAddress, types.AllowEntry) (bool, error) {
		empty = false
		return true, nil
	})
	return empty, err
}

// jailIfBonded jails operator's validator if it exists and is not jailed already.
func (k *Keeper) jailIfBonded(ctx context.Context, operator sdk.AccAddress) error {
	v, err := k.staking.GetValidator(ctx, sdk.ValAddress(operator))
	if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
		return nil // allowlisted but never created: nothing to jail
	}
	if err != nil {
		return err
	}
	if v.IsJailed() {
		return nil
	}
	cons, err := v.GetConsAddr()
	if err != nil {
		return err
	}
	if err := k.staking.Jail(ctx, cons); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeValidatorJailed, sdk.NewAttribute(types.AttributeKeyOperator, operator.String()),
	))
	return nil
}

// Exec applies MsgExec: every inner message must be an accepted type whose authority
// is the x/admin module account; each is dispatched in order and any failure fails
// the whole message.
func (k *Keeper) Exec(ctx context.Context, msg *types.MsgExec) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.checkAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	if k.router == nil {
		return errorsmod.Wrap(types.ErrExecMsg, "no router")
	}
	msgs, err := msg.SdkMsgs()
	if err != nil {
		return errorsmod.Wrap(types.ErrExecMsg, err.Error())
	}
	authority := ModuleAddress().String()
	for i, m := range msgs {
		url := sdk.MsgTypeURL(m)
		if _, ok := slices.BinarySearch(k.execTypes, url); !ok {
			return errorsmod.Wrapf(types.ErrExecMsg, "message %d: %s", i, url)
		}
		a, ok := m.(interface{ GetAuthority() string })
		if !ok || a.GetAuthority() != authority {
			return errorsmod.Wrapf(types.ErrExecMsg, "message %d: authority must be %s", i, authority)
		}
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	for i, m := range msgs {
		if v, ok := m.(sdk.HasValidateBasic); ok {
			if err := v.ValidateBasic(); err != nil {
				return errorsmod.Wrapf(err, "message %d", i)
			}
		}
		handler := k.router.Handler(m)
		if handler == nil {
			return errorsmod.Wrapf(types.ErrExecMsg, "message %d: no handler for %s", i, sdk.MsgTypeURL(m))
		}
		res, err := handler(sdkCtx, m)
		if err != nil {
			return errorsmod.Wrapf(err, "message %d", i)
		}
		sdkCtx.EventManager().EmitEvents(eventsOf(res))
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeAdminExec, sdk.NewAttribute(types.AttributeKeyMsgType, sdk.MsgTypeURL(m))))
	}
	return nil
}

func eventsOf(res *sdk.Result) sdk.Events {
	if res == nil {
		return nil
	}
	out := make(sdk.Events, len(res.Events))
	for i, e := range res.Events {
		out[i] = sdk.Event(e)
	}
	return out
}

// UpdateParams applies MsgUpdateParams. Replacing the admin emits admin_changed.
func (k *Keeper) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.checkAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	old, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if err := k.Params.Set(ctx, msg.Params); err != nil {
		return err
	}
	if old.Admin != msg.Params.Admin {
		sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
			types.EventTypeAdminChanged,
			sdk.NewAttribute(types.AttributeKeyOld, old.Admin),
			sdk.NewAttribute(types.AttributeKeyNew, msg.Params.Admin),
		))
	}
	return nil
}

// CheckAdmin returns ErrUnauthorized unless signer is the current admin. Other
// Keystone modules use it for their MsgUpdateParams.
func (k *Keeper) CheckAdmin(ctx context.Context, signer string) error {
	return k.checkAdmin(ctx, signer)
}

func (k *Keeper) checkAdmin(ctx context.Context, signer string) error {
	admin, err := k.Admin(ctx)
	if err != nil {
		return err
	}
	s, err := sdk.AccAddressFromBech32(signer)
	if err != nil {
		return errorsmod.Wrapf(types.ErrInvalidAddress, "signer %q: %v", signer, err)
	}
	if !admin.Equals(s) {
		return errorsmod.Wrapf(types.ErrUnauthorized, "expected %s, got %s", admin, s)
	}
	return nil
}

// InitGenesis stores params and the allowlist.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) error {
	if err := gs.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return err
	}
	for _, e := range gs.Allowlist {
		if err := k.Allowlist.Set(ctx, sdk.MustAccAddressFromBech32(e.Operator), e); err != nil {
			return err
		}
	}
	return nil
}

// ExportGenesis returns params and the allowlist.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := k.AllEntries(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p, Allowlist: entries}, nil
}

// GetEntry returns one allowlist entry.
func (k *Keeper) GetEntry(ctx context.Context, operator sdk.AccAddress) (types.AllowEntry, error) {
	e, err := k.Allowlist.Get(ctx, operator)
	if errors.Is(err, collections.ErrNotFound) {
		return e, errorsmod.Wrapf(types.ErrNotAllowlisted, "%s", operator)
	}
	return e, err
}
