package keeper

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtprotocrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/x/poa/types"
)

// Keeper owns the admin, the operator records and the validator updates sent to CometBFT.
type Keeper struct {
	Schema      collections.Schema
	Params      collections.Item[types.Params]
	Operators   collections.Map[sdk.AccAddress, types.Operator]
	ConsIndex   collections.Map[sdk.ConsAddress, []byte] // value: operator address bytes
	UsedPubKeys collections.KeySet[[]byte]
	LastPower   collections.Map[sdk.AccAddress, int64] // power last sent to CometBFT

	guard types.RemovalGuard
}

// NewKeeper builds the keeper with the AllowAll removal guard.
func NewKeeper(cdc codec.BinaryCodec, storeService store.KVStoreService) *Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := &Keeper{
		Params:      collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Operators:   collections.NewMap(sb, types.OperatorsKey, "operators", sdk.AccAddressKey, codec.CollValue[types.Operator](cdc)),
		ConsIndex:   collections.NewMap(sb, types.ConsIndexKey, "cons_index", sdk.ConsAddressKey, collections.BytesValue),
		UsedPubKeys: collections.NewKeySet(sb, types.UsedPubKeysKey, "used_pubkeys", collections.BytesKey),
		LastPower:   collections.NewMap(sb, types.LastPowerKey, "last_power", sdk.AccAddressKey, collections.Int64Value),
		guard:       types.AllowAll{},
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// SetRemovalGuard replaces the removal guard (x/custody installs its guard in M5).
func (k *Keeper) SetRemovalGuard(g types.RemovalGuard) { k.guard = g }

// Admin returns the admin address from params.
func (k *Keeper) Admin(ctx context.Context) (sdk.AccAddress, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return sdk.AccAddressFromBech32(p.Admin)
}

// GetOperator returns the record for an operator address.
func (k *Keeper) GetOperator(ctx context.Context, addr sdk.AccAddress) (types.Operator, error) {
	op, err := k.Operators.Get(ctx, addr)
	if errors.Is(err, collections.ErrNotFound) {
		return op, errorsmod.Wrapf(types.ErrUnknownOperator, "%s", addr)
	}
	return op, err
}

// AllOperators returns every record, power 0 included, sorted by operator address.
func (k *Keeper) AllOperators(ctx context.Context) ([]types.Operator, error) {
	var out []types.Operator
	err := k.Operators.Walk(ctx, nil, func(_ sdk.AccAddress, op types.Operator) (bool, error) {
		out = append(out, op)
		return false, nil
	})
	return out, err
}

// ActiveValidators returns operators with power > 0, sorted by operator address.
func (k *Keeper) ActiveValidators(ctx context.Context) ([]types.Operator, error) {
	all, err := k.AllOperators(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(op types.Operator) bool { return op.Power <= 0 }), nil
}

// TotalPower sums the power of every operator.
func (k *Keeper) TotalPower(ctx context.Context) (int64, error) {
	var total int64
	err := k.Operators.Walk(ctx, nil, func(_ sdk.AccAddress, op types.Operator) (bool, error) {
		total += op.Power
		return false, nil
	})
	return total, err
}

// GetPubKeyByConsAddr implements baseapp.ValidatorStore. It succeeds for operators at
// power 0 too, so VEs from the last heights before a removal still verify.
func (k *Keeper) GetPubKeyByConsAddr(ctx context.Context, addr sdk.ConsAddress) (cmtprotocrypto.PublicKey, error) {
	opAddr, err := k.ConsIndex.Get(ctx, addr)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return cmtprotocrypto.PublicKey{}, errorsmod.Wrapf(types.ErrUnknownOperator, "consensus address %s", addr)
		}
		return cmtprotocrypto.PublicKey{}, err
	}
	op, err := k.Operators.Get(ctx, opAddr)
	if err != nil {
		return cmtprotocrypto.PublicKey{}, err
	}
	return pubKeyProto(op.ConsensusPubkey), nil
}

func pubKeyProto(pk []byte) cmtprotocrypto.PublicKey {
	return cmtprotocrypto.PublicKey{Sum: &cmtprotocrypto.PublicKey_Ed25519{Ed25519: slices.Clone(pk)}}
}

// setOperator writes a record and its indexes.
func (k *Keeper) setOperator(ctx context.Context, addr sdk.AccAddress, op types.Operator) error {
	if err := k.Operators.Set(ctx, addr, op); err != nil {
		return err
	}
	if err := k.ConsIndex.Set(ctx, op.ConsAddress(), addr); err != nil {
		return err
	}
	return k.UsedPubKeys.Set(ctx, op.ConsensusPubkey)
}

// SetValidators applies MsgSetValidators after ValidateBasic. Rules (design §4.2):
// signer is the admin; a new operator brings a never-used pubkey; an existing operator
// keeps its pubkey; the removal guard must allow the change; at least one operator
// stays active. Records are never deleted.
func (k *Keeper) SetValidators(ctx context.Context, msg *types.MsgSetValidators) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.checkAdmin(ctx, msg.Admin); err != nil {
		return err
	}

	before, err := k.AllOperators(ctx)
	if err != nil {
		return err
	}

	type change struct {
		addr sdk.AccAddress
		op   types.Operator
	}
	changes := make([]change, 0, len(msg.Entries))
	var added, removed []string
	for _, e := range msg.Entries {
		addr := sdk.MustAccAddressFromBech32(e.OperatorAddress)
		next := types.Operator{
			OperatorAddress: addr.String(),
			ConsensusPubkey: slices.Clone(e.ConsensusPubkey),
			Moniker:         e.Moniker,
			Power:           e.Power,
		}
		prev, err := k.Operators.Get(ctx, addr)
		switch {
		case errors.Is(err, collections.ErrNotFound):
			used, err := k.UsedPubKeys.Has(ctx, e.ConsensusPubkey)
			if err != nil {
				return err
			}
			if used {
				return errorsmod.Wrapf(types.ErrPubKeyUsed, "operator %s", next.OperatorAddress)
			}
			if next.Power > 0 {
				added = append(added, next.OperatorAddress)
			}
		case err != nil:
			return err
		default:
			if !bytes.Equal(prev.ConsensusPubkey, e.ConsensusPubkey) {
				return errorsmod.Wrapf(types.ErrPubKeyChange, "operator %s", next.OperatorAddress)
			}
			if prev.Power == 0 && next.Power > 0 {
				added = append(added, next.OperatorAddress)
			}
			if prev.Power > 0 && next.Power == 0 {
				removed = append(removed, next.OperatorAddress)
			}
		}
		changes = append(changes, change{addr, next})
	}

	after := mergeOperators(before, changes, func(c change) types.Operator { return c.op })
	if !slices.ContainsFunc(after, func(op types.Operator) bool { return op.Power > 0 }) {
		return types.ErrEmptyValidatorSet
	}
	if err := k.guard.AllowSetValidators(ctx, before, after); err != nil {
		return errorsmod.Wrap(types.ErrGuardRefused, err.Error())
	}

	for _, c := range changes {
		if err := k.setOperator(ctx, c.addr, c.op); err != nil {
			return err
		}
	}

	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeValidatorSetUpdated,
		sdk.NewAttribute(types.AttributeKeyAdded, strings.Join(added, ",")),
		sdk.NewAttribute(types.AttributeKeyRemoved, strings.Join(removed, ",")),
	))
	return nil
}

// mergeOperators returns before with the changed records replaced or inserted, sorted
// by operator address bytes.
func mergeOperators[C any](before []types.Operator, changes []C, op func(C) types.Operator) []types.Operator {
	out := slices.Clone(before)
	for _, c := range changes {
		next := op(c)
		i := slices.IndexFunc(out, func(o types.Operator) bool { return o.OperatorAddress == next.OperatorAddress })
		if i >= 0 {
			out[i] = next
		} else {
			out = append(out, next)
		}
	}
	slices.SortFunc(out, func(a, b types.Operator) int {
		return bytes.Compare(sdk.MustAccAddressFromBech32(a.OperatorAddress), sdk.MustAccAddressFromBech32(b.OperatorAddress))
	})
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

// ApplyAndReturnValidatorSetUpdates compares each operator's power with the power last
// sent to CometBFT and returns updates for the differences, sorted by consensus
// address. It records the new powers. Used by EndBlocker and InitGenesis, and by
// x/genutil as its StakingKeeper.
func (k *Keeper) ApplyAndReturnValidatorSetUpdates(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	type upd struct {
		addr sdk.AccAddress
		cons sdk.ConsAddress
		vu   abci.ValidatorUpdate
	}
	var updates []upd
	err := k.Operators.Walk(ctx, nil, func(addr sdk.AccAddress, op types.Operator) (bool, error) {
		last, err := k.LastPower.Get(ctx, addr)
		if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return true, err
		}
		if last != op.Power {
			vu := abci.ValidatorUpdate{PubKey: pubKeyProto(op.ConsensusPubkey), Power: op.Power}
			updates = append(updates, upd{addr, op.ConsAddress(), vu})
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	for _, u := range updates {
		if err := k.LastPower.Set(ctx, u.addr, u.vu.Power); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(updates, func(a, b upd) int { return bytes.Compare(a.cons, b.cons) })
	out := make([]abci.ValidatorUpdate, len(updates))
	for i, u := range updates {
		out[i] = u.vu
	}
	return out, nil
}

// EndBlocker returns the validator updates for this block.
func (k *Keeper) EndBlocker(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	return k.ApplyAndReturnValidatorSetUpdates(ctx)
}

// InitGenesis stores params and operators and returns the initial validator set.
func (k *Keeper) InitGenesis(ctx context.Context, gs *types.GenesisState) ([]abci.ValidatorUpdate, error) {
	if err := gs.Validate(); err != nil {
		return nil, err
	}
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		return nil, err
	}
	for _, op := range gs.Operators {
		if err := k.setOperator(ctx, sdk.MustAccAddressFromBech32(op.OperatorAddress), op); err != nil {
			return nil, err
		}
	}
	return k.ApplyAndReturnValidatorSetUpdates(ctx)
}

// ExportGenesis returns params and every operator record.
func (k *Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	ops, err := k.AllOperators(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GenesisState{Params: p, Operators: ops}, nil
}
