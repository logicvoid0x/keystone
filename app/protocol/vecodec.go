// Package protocol holds Keystone's ABCI++ logic (design §5): the VE codec, tx0
// injection, vote verification, proposal handlers and the PreBlocker orchestrator.
// It is state-machine code: deterministic, no I/O, no goroutines.
package protocol

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
)

const (
	// VEVersion is the first VE version; an upgrade may move later heights to
	// VEVersion + 1 (design §17, Schedule).
	VEVersion = 1
	// MaxVEBytes caps an encoded VE (design §5.3).
	MaxVEBytes = 16384

	// Per-payload count caps checked by WithinCounts. MaxDeposits is from design §9.7;
	// the others are sized from the §5.3 byte budget.
	MaxDeposits    = 16
	MaxConfirms    = 16
	MaxReorgs      = 16
	MaxSpends      = 8 // design §9.12 (M7)
	MaxCommitments = 128
	MaxShares      = 64
)

var (
	errOversize     = errors.New("vote extension too large")
	errVersion      = errors.New("wrong vote extension version")
	errHeight       = errors.New("vote extension height does not match the vote")
	errNotCanonical = errors.New("vote extension is not canonical")
	errCounts       = errors.New("vote extension exceeds a count limit")
)

// Schedule gives the VE version for votes at a height (design §17). FixedV1 is the
// schedule without upgrades.
type Schedule func(ctx context.Context, height int64) uint32

// FixedV1 is version 1 at every height.
func FixedV1(context.Context, int64) uint32 { return VEVersion }

// DecodeVE is DecodeVEAt with version 1.
func DecodeVE(bz []byte, height int64) (vetypes.VoteExtension, error) {
	return DecodeVEAt(bz, height, VEVersion)
}

// DecodeVEAt decodes and checks a VE for a vote at height: size, decoding, version,
// height, canonical order and counts (design §5.2). The encoding must also be the one
// Marshal produces, so each VE has exactly one byte form.
func DecodeVEAt(bz []byte, height int64, version uint32) (vetypes.VoteExtension, error) {
	var ve vetypes.VoteExtension
	if len(bz) > MaxVEBytes {
		return ve, errOversize
	}
	if err := ve.Unmarshal(bz); err != nil {
		return ve, fmt.Errorf("decode vote extension: %w", err)
	}
	if ve.Version != version {
		return ve, errVersion
	}
	if ve.Height != height {
		return ve, errHeight
	}
	if !IsCanonical(&ve) {
		return ve, errNotCanonical
	}
	if !WithinCounts(&ve) {
		return ve, errCounts
	}
	re, err := ve.Marshal()
	if err != nil || !bytes.Equal(re, bz) {
		return ve, errNotCanonical
	}
	return ve, nil
}

// MinimalVE is the encoded version-1 VE with no payloads.
func MinimalVE(height int64) []byte { return MinimalVEAt(height, VEVersion) }

// MinimalVEAt is the encoded VE of version with no payloads.
func MinimalVEAt(height int64, version uint32) []byte {
	ve := vetypes.VoteExtension{Version: version, Height: height}
	bz, err := ve.Marshal()
	if err != nil {
		panic(err) // two scalar fields cannot fail to marshal
	}
	return bz
}

// Canonicalize sorts every repeated field into canonical order.
func Canonicalize(ve *vetypes.VoteExtension) {
	if b := ve.Bitcoin; b != nil {
		slices.SortStableFunc(b.Deposits, cmpDeposit)
		slices.SortStableFunc(b.Confirms, cmpConfirm)
		slices.SortStableFunc(b.Reorgs, cmpReorg)
		slices.SortStableFunc(b.Spends, cmpSpend)
	}
	if f := ve.Frost; f != nil {
		slices.SortStableFunc(f.Commitments, cmpCommitment)
		slices.SortStableFunc(f.Shares, cmpShare)
	}
}

// IsCanonical reports whether every repeated field is strictly ascending in canonical
// order (sorted, no duplicate keys).
func IsCanonical(ve *vetypes.VoteExtension) bool {
	if b := ve.Bitcoin; b != nil {
		if !strictlySorted(b.Deposits, cmpDeposit) || !strictlySorted(b.Confirms, cmpConfirm) ||
			!strictlySorted(b.Reorgs, cmpReorg) || !strictlySorted(b.Spends, cmpSpend) {
			return false
		}
	}
	if f := ve.Frost; f != nil {
		if !strictlySorted(f.Commitments, cmpCommitment) || !strictlySorted(f.Shares, cmpShare) {
			return false
		}
	}
	return true
}

// WithinCounts reports whether every repeated field is within its cap.
func WithinCounts(ve *vetypes.VoteExtension) bool {
	if b := ve.Bitcoin; b != nil {
		if len(b.Deposits) > MaxDeposits || len(b.Confirms) > MaxConfirms || len(b.Reorgs) > MaxReorgs ||
			len(b.Spends) > MaxSpends {
			return false
		}
	}
	if f := ve.Frost; f != nil {
		if len(f.Commitments) > MaxCommitments || len(f.Shares) > MaxShares {
			return false
		}
	}
	return true
}

func strictlySorted[T any](xs []T, c func(a, b T) int) bool {
	for i := 1; i < len(xs); i++ {
		if c(xs[i-1], xs[i]) >= 0 {
			return false
		}
	}
	return true
}

func cmpDeposit(a, b vetypes.DepositSeen) int {
	return cmp.Or(bytes.Compare(a.Txid, b.Txid), cmp.Compare(a.Vout, b.Vout))
}

func cmpConfirm(a, b vetypes.TxConfirmed) int { return bytes.Compare(a.Txid, b.Txid) }

func cmpReorg(a, b vetypes.ReorgSeen) int {
	return cmp.Or(bytes.Compare(a.Txid, b.Txid), cmp.Compare(a.Vout, b.Vout))
}

func cmpSpend(a, b vetypes.UnauthorizedSpend) int {
	return cmp.Or(bytes.Compare(a.PrevTxid, b.PrevTxid), cmp.Compare(a.PrevVout, b.PrevVout))
}

func cmpCommitment(a, b vetypes.NonceCommitment) int {
	return cmp.Or(cmp.Compare(a.Epoch, b.Epoch), cmp.Compare(a.ShareIndex, b.ShareIndex), cmp.Compare(a.Seq, b.Seq))
}

func cmpShare(a, b vetypes.CombinedShare) int { return bytes.Compare(a.SessionId, b.SessionId) }

// at is the version for height (version 1 without a schedule).
func (s Schedule) at(ctx context.Context, height int64) uint32 {
	if s == nil {
		return VEVersion
	}
	return s(ctx, height)
}
