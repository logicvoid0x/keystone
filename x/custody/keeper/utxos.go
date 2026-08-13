package keeper

import (
	"context"
	"encoding/binary"
	"math"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/OWNER/keystone/x/custody/types"
)

// OutpointBytes is txid ‖ vout (4 bytes big-endian), the outpoint part of index keys.
func OutpointBytes(txid []byte, vout uint32) []byte {
	return append(append([]byte(nil), txid...), byte(vout>>24), byte(vout>>16), byte(vout>>8), byte(vout))
}

func utxoIndexKey(u types.Utxo) collections.Triple[int32, uint64, []byte] {
	return collections.Join3(int32(u.Status), math.MaxUint64-u.AmountSat, OutpointBytes(u.Txid, u.Vout))
}

// setUtxo stores u and its coin-selection index entry (status, inverted amount,
// outpoint), replacing prev's entry if given.
func (k *Keeper) setUtxo(ctx context.Context, prev *types.Utxo, u types.Utxo) error {
	if prev != nil {
		if err := k.UtxoIndex.Remove(ctx, utxoIndexKey(*prev)); err != nil {
			return err
		}
	}
	if err := k.Utxos.Set(ctx, collections.Join(u.Txid, u.Vout), u); err != nil {
		return err
	}
	return k.UtxoIndex.Set(ctx, utxoIndexKey(u))
}

// removeUtxo deletes u and its index entry.
func (k *Keeper) removeUtxo(ctx context.Context, u types.Utxo) error {
	if err := k.UtxoIndex.Remove(ctx, utxoIndexKey(u)); err != nil {
		return err
	}
	return k.Utxos.Remove(ctx, collections.Join(u.Txid, u.Vout))
}

// UtxoSum is the satoshis held in Keystone UTXOs (AVAILABLE and LOCKED).
func (k *Keeper) UtxoSum(ctx context.Context) (sdkmath.Int, error) {
	total := sdkmath.ZeroInt()
	err := k.Utxos.Walk(ctx, nil, func(_ collections.Pair[[]byte, uint32], u types.Utxo) (bool, error) {
		total = total.Add(sdkmath.NewIntFromUint64(u.AmountSat))
		return false, nil
	})
	return total, err
}

// SplitOutpoint is the inverse of OutpointBytes.
func SplitOutpoint(op []byte) ([]byte, uint32, bool) {
	if len(op) != types.HashLen+4 {
		return nil, 0, false
	}
	return op[:types.HashLen], binary.BigEndian.Uint32(op[types.HashLen:]), true
}
