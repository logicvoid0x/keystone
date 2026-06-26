package types

import (
	"cmp"
	"fmt"
	"regexp"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesis holds the default params and no key epoch.
func DefaultGenesis() *GenesisState { return &GenesisState{Params: DefaultParams()} }

var localChainID = regexp.MustCompile(`-local-[0-9]+$`)

// IsLocalChainID reports whether chainID ends in -local-<n>. Only such chains may start
// with a custody epoch in genesis (the `--dev-dealer` shortcut, design §9.3).
func IsLocalChainID(chainID string) bool { return localChainID.MatchString(chainID) }

// Validate checks the params and the epoch entries: epochs strictly ascending by
// number, at most one ACTIVE, active_epoch names it (or is 0 when none is ACTIVE),
// group keys even-y compressed; share indices and verifying shares strictly ascending by
// (epoch, index), indices 1..n of a known epoch, exactly n of each per epoch.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	bad := func(format string, a ...any) error {
		return errorsmod.Wrapf(ErrInvalidGenesis, format, a...)
	}
	epochs := map[uint64]KeyEpoch{}
	active := uint64(0)
	for i, e := range gs.Epochs {
		if e.Number == 0 || (i > 0 && e.Number <= gs.Epochs[i-1].Number) {
			return bad("epochs must be ascending from 1")
		}
		if len(e.GroupKey) != 33 || e.GroupKey[0] != 0x02 {
			return bad("epoch %d: group key must be 33-byte compressed with even y", e.Number)
		}
		if _, _, err := ParseGroupKey(e.GroupKey); err != nil {
			return bad("epoch %d: %v", e.Number, err)
		}
		if e.N == 0 || e.T == 0 || e.T > e.N {
			return bad("epoch %d: bad n=%d t=%d", e.Number, e.N, e.T)
		}
		if e.Status == EPOCH_STATUS_ACTIVE {
			if active != 0 {
				return bad("more than one ACTIVE epoch")
			}
			active = e.Number
		}
		epochs[e.Number] = e
	}
	if gs.ActiveEpoch != active {
		return bad("active_epoch %d, ACTIVE epoch is %d", gs.ActiveEpoch, active)
	}
	shareCount := map[uint64]uint32{}
	for i, s := range gs.ShareIndices {
		e, ok := epochs[s.Epoch]
		if !ok || s.Index == 0 || s.Index > e.N {
			return bad("share index %d of epoch %d", s.Index, s.Epoch)
		}
		if i > 0 && cmpEpochIndex(gs.ShareIndices[i-1].Epoch, gs.ShareIndices[i-1].Index, s.Epoch, s.Index) >= 0 {
			return bad("share indices must be sorted by (epoch, index)")
		}
		if _, err := sdk.AccAddressFromBech32(s.Operator); err != nil {
			return bad("share index operator: %v", err)
		}
		shareCount[s.Epoch]++
	}
	vsCount := map[uint64]uint32{}
	for i, v := range gs.VerifyingShares {
		e, ok := epochs[v.Epoch]
		if !ok || v.Index == 0 || v.Index > e.N {
			return bad("verifying share %d of epoch %d", v.Index, v.Epoch)
		}
		if i > 0 && cmpEpochIndex(gs.VerifyingShares[i-1].Epoch, gs.VerifyingShares[i-1].Index, v.Epoch, v.Index) >= 0 {
			return bad("verifying shares must be sorted by (epoch, index)")
		}
		if _, _, err := ParseGroupKey(v.Pubkey); err != nil || len(v.Pubkey) != 33 {
			return bad("verifying share %d of epoch %d: bad point", v.Index, v.Epoch)
		}
		vsCount[v.Epoch]++
	}
	for _, e := range gs.Epochs {
		if shareCount[e.Number] != e.N || vsCount[e.Number] != e.N {
			return bad("epoch %d needs %d share indices and verifying shares", e.Number, e.N)
		}
	}
	return nil
}

func cmpEpochIndex(ea uint64, ia uint32, eb uint64, ib uint32) int {
	return cmp.Or(cmp.Compare(ea, eb), cmp.Compare(ia, ib))
}

// CheckChainID rejects genesis epochs on a non-localnet chain id.
func (gs GenesisState) CheckChainID(chainID string) error {
	if len(gs.Epochs) > 0 && !IsLocalChainID(chainID) {
		return fmt.Errorf("custody genesis epochs are only allowed on localnet chain ids (-local-<n>), not %q", chainID)
	}
	return nil
}
