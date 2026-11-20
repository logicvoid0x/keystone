// FLAG FOR AUDIT: unauthorized-spend slashing (design §9.12, M7).

package keeper

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/x/custody/types"
)

// SetSlasher attaches the staking slasher (the app adapter over x/staking and
// x/slashing). Without one, theft slashing only removes the UTXO and pauses deposits.
func (k *Keeper) SetSlasher(s types.StakingSlasher) { k.slasher = s }

// slashSpend is §9.12 step 3 for an accepted UnauthorizedSpend: the UTXO leaves the
// UTXO set and its amount goes to the custody deficit (invariant 6 keeps holding);
// every share holder of its epoch is slashed, jailed and tombstoned, once per epoch;
// deposits pause. A member that cannot be slashed is skipped with an event.
func (k *Keeper) slashSpend(ctx context.Context, s vetypes.UnauthorizedSpend) error {
	u, err := k.Utxos.Get(ctx, collections.Join(s.PrevTxid, s.PrevVout))
	if errors.Is(err, collections.ErrNotFound) {
		return errSkip("not a Keystone UTXO")
	} else if err != nil {
		return err
	}
	if has, err := k.TxBatch.Has(ctx, s.SpendingTxid); err != nil || has {
		return orSkip(err, "spent by a Keystone batch transaction")
	}
	if err := k.removeUtxo(ctx, u); err != nil {
		return err
	}
	def, err := k.Deficit.Get(ctx)
	if err != nil {
		return err
	}
	if err := k.Deficit.Set(ctx, def.Add(sdkmath.NewIntFromUint64(u.AmountSat))); err != nil {
		return err
	}
	if err := k.TheftPaused.Set(ctx, true); err != nil {
		return err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	members, err := k.epochOperators(ctx, u.Epoch)
	if err != nil {
		return err
	}
	already, err := k.SlashedEpochs.Has(ctx, u.Epoch)
	if err != nil {
		return err
	}
	burned := sdkmath.ZeroInt()
	if !already {
		if err := k.SlashedEpochs.Set(ctx, u.Epoch, sdkCtx.BlockHeight()); err != nil {
			return err
		}
		epoch, err := k.Epochs.Get(ctx, u.Epoch)
		if err != nil {
			return err
		}
		p, err := k.Params.Get(ctx)
		if err != nil {
			return err
		}
		infraction := max(epoch.StartHeight, 1)
		for _, m := range members {
			n, err := k.slashMember(ctx, m, infraction, p.TheftSlashFraction)
			if err != nil {
				sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeSlashSkipped,
					sdk.NewAttribute(types.AttributeKeyOperator, m.String()),
					sdk.NewAttribute(types.AttributeKeyReason, err.Error())))
				continue
			}
			burned = burned.Add(n)
		}
	}
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.String()
	}
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeTheftSlashed,
		sdk.NewAttribute(types.AttributeKeyEpoch, strconv.FormatUint(u.Epoch, 10)),
		sdk.NewAttribute(types.AttributeKeyOutpoint, types.OutpointString(s.PrevTxid, s.PrevVout)),
		sdk.NewAttribute(types.AttributeKeySpendingTxid, types.TxidString(s.SpendingTxid)),
		sdk.NewAttribute(types.AttributeKeyMembers, strings.Join(names, ",")),
		sdk.NewAttribute(types.AttributeKeySlashed, burned.String()),
		sdk.NewAttribute(types.AttributeKeySat, strconv.FormatUint(u.AmountSat, 10))))
	return nil
}

// slashMember slashes one member in its own cache context, so a failure leaves no
// partial writes.
func (k *Keeper) slashMember(ctx context.Context, op sdk.AccAddress, infraction int64, fraction sdkmath.LegacyDec) (sdkmath.Int, error) {
	if k.slasher == nil {
		return sdkmath.ZeroInt(), errors.New("no slasher")
	}
	cc, write := sdk.UnwrapSDKContext(ctx).CacheContext()
	n, err := k.slasher.SlashForTheft(cc, op, infraction, fraction)
	if err != nil {
		return sdkmath.ZeroInt(), err
	}
	write()
	return n, nil
}

// IsTheftPaused reports whether deposits are paused after a theft (§9.12).
func (k *Keeper) IsTheftPaused(ctx context.Context) (bool, error) {
	paused, err := k.TheftPaused.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	return paused, err
}

// ClearTheftPause applies MsgClearTheftPause: allowed once no slashed epoch is ACTIVE.
func (k *Keeper) ClearTheftPause(ctx context.Context, msg *types.MsgClearTheftPause) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.admin.CheckAdmin(ctx, msg.Admin); err != nil {
		return err
	}
	active, err := k.ActiveEpoch.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if active > 0 {
		if slashed, err := k.SlashedEpochs.Has(ctx, active); err != nil {
			return err
		} else if slashed {
			return errorsmod.Wrapf(types.ErrTheftPaused, "epoch %d", active)
		}
	}
	if err := k.TheftPaused.Set(ctx, false); err != nil {
		return err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeTheftPauseCleared))
	return nil
}
