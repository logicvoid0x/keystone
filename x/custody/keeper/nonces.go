// FLAG FOR AUDIT: on-chain nonce pools (design §9.9 rules 2 and 6).

package keeper

import (
	"context"
	"errors"
	"strconv"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/x/custody/types"
)

// MaxNewCommitmentsPerBlock is how many commitments a share may add per block (§9.9
// rule 6).
const MaxNewCommitmentsPerBlock = 2

// recordCommitments is the RecordCommitments op for one voting operator (§9.9):
// revoke_all_nonces first discards every unconsumed commitment of the operator's
// shares; then each commitment is recorded if the share is the operator's in an ACTIVE
// or RETIRING epoch, its seq is above the last recorded one, the share has added fewer
// than MaxNewCommitmentsPerBlock this block and holds fewer than NoncePool, and both
// points decode. Others are ignored.
func (k *Keeper) recordCommitments(ctx context.Context, in types.FrostInput) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if in.Payload.RevokeAllNonces {
		if err := k.revokeNonces(ctx, in.Operator); err != nil {
			return err
		}
	}
	added := map[[2]uint64]int{}
	for _, c := range in.Payload.Commitments {
		e, err := k.Epochs.Get(ctx, c.Epoch)
		if errors.Is(err, collections.ErrNotFound) {
			continue
		} else if err != nil {
			return err
		}
		if e.Status != types.EPOCH_STATUS_ACTIVE && e.Status != types.EPOCH_STATUS_RETIRING {
			continue
		}
		idx, err := k.ShareIndex.Get(ctx, collections.Join(c.Epoch, in.Operator))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		} else if err != nil {
			return err
		}
		if idx != c.ShareIndex {
			continue
		}
		key := [2]uint64{c.Epoch, uint64(idx)}
		if added[key] >= MaxNewCommitmentsPerBlock {
			continue
		}
		last, err := k.lastRecorded(ctx, c.Epoch, idx)
		if err != nil {
			return err
		}
		if c.Seq <= last {
			continue
		}
		n, err := k.poolSize(ctx, c.Epoch, idx)
		if err != nil {
			return err
		}
		if n >= int(p.NoncePool) {
			continue
		}
		if _, err := frost.ParseCommitment(idx, c.Hiding, c.Binding); err != nil {
			continue
		}
		if err := k.Commitments.Set(ctx, collections.Join3(c.Epoch, idx, c.Seq), types.StoredCommitment{Hiding: c.Hiding, Binding: c.Binding}); err != nil {
			return err
		}
		if err := k.PoolHead.Set(ctx, collections.Join(c.Epoch, idx), c.Seq); err != nil {
			return err
		}
		added[key]++
	}
	return nil
}

// revokeNonces discards every unconsumed commitment of op's shares (§9.5 restore).
func (k *Keeper) revokeNonces(ctx context.Context, op sdk.AccAddress) error {
	var shares []collections.Pair[uint64, uint32]
	err := k.ShareIndex.Walk(ctx, nil, func(key collections.Pair[uint64, sdk.AccAddress], idx uint32) (bool, error) {
		if key.K2().Equals(op) {
			shares = append(shares, collections.Join(key.K1(), idx))
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	n := 0
	for _, s := range shares {
		var keys []collections.Triple[uint64, uint32, uint64]
		rng := collections.NewSuperPrefixedTripleRange[uint64, uint32, uint64](s.K1(), s.K2())
		if err := k.Commitments.Walk(ctx, rng, func(key collections.Triple[uint64, uint32, uint64], _ types.StoredCommitment) (bool, error) {
			keys = append(keys, key)
			return false, nil
		}); err != nil {
			return err
		}
		for _, key := range keys {
			if err := k.Commitments.Remove(ctx, key); err != nil {
				return err
			}
		}
		n += len(keys)
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeNoncesRevoked,
		sdk.NewAttribute(types.AttributeKeyOperator, op.String()),
		sdk.NewAttribute(types.AttributeKeyID, strconv.Itoa(n))))
	return nil
}

func (k *Keeper) lastRecorded(ctx context.Context, epoch uint64, share uint32) (uint64, error) {
	v, err := k.PoolHead.Get(ctx, collections.Join(epoch, share))
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	return v, err
}

func (k *Keeper) poolSize(ctx context.Context, epoch uint64, share uint32) (int, error) {
	n := 0
	rng := collections.NewSuperPrefixedTripleRange[uint64, uint32, uint64](epoch, share)
	err := k.Commitments.Walk(ctx, rng, func(_ collections.Triple[uint64, uint32, uint64], _ types.StoredCommitment) (bool, error) {
		n++
		return false, nil
	})
	return n, err
}

// NoncePool returns a share's last recorded seq and its unconsumed commitments in seq
// order (for queries and the signer).
func (k *Keeper) NoncePool(ctx context.Context, epoch uint64, share uint32) (uint64, []vetypes.NonceCommitment, error) {
	last, err := k.lastRecorded(ctx, epoch, share)
	if err != nil {
		return 0, nil, err
	}
	var out []vetypes.NonceCommitment
	rng := collections.NewSuperPrefixedTripleRange[uint64, uint32, uint64](epoch, share)
	err = k.Commitments.Walk(ctx, rng, func(key collections.Triple[uint64, uint32, uint64], c types.StoredCommitment) (bool, error) {
		out = append(out, vetypes.NonceCommitment{Epoch: epoch, ShareIndex: share, Seq: key.K3(), Hiding: c.Hiding, Binding: c.Binding})
		return false, nil
	})
	return last, out, err
}
