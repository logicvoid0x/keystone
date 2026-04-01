// Package protocol holds Keystone's ABCI++ logic (design §5): the VE codec, tx0
// injection, vote verification, proposal handlers and the PreBlocker orchestrator.
// It is state-machine code: deterministic, no I/O, no goroutines.
package protocol

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

const (
	// VEVersion is the only VE version in v1.
	VEVersion = 1
	// MaxVEBytes caps an encoded VE (design §5.3).
	MaxVEBytes = 16384

	// Per-payload count caps checked by WithinCounts. MaxDeposits is from design §9.7;
	// the others are sized from the §5.3 byte budget (docs/decisions.md).
	MaxDeposits    = 16
	MaxConfirms    = 16
	MaxReorgs      = 16
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

// DecodeVE decodes and checks a VE for a vote at height: size, decoding, version,
// height, canonical order and counts (design §5.2). The encoding must also be the one
// Marshal produces, so each VE has exactly one byte form.
func DecodeVE(bz []byte, height int64) (vetypes.VoteExtension, error) {
	var ve vetypes.VoteExtension
	if len(bz) > MaxVEBytes {
		return ve, errOversize
	}
	if err := ve.Unmarshal(bz); err != nil {
		return ve, fmt.Errorf("decode vote extension: %w", err)
	}
	if ve.Version != VEVersion {
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

// MinimalVE is the encoded VE with no payloads.
func MinimalVE(height int64) []byte {
	ve := vetypes.VoteExtension{Version: VEVersion, Height: height}
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
			!strictlySorted(b.Reorgs, cmpReorg) {
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
		if len(b.Deposits) > MaxDeposits || len(b.Confirms) > MaxConfirms || len(b.Reorgs) > MaxReorgs {
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

func cmpCommitment(a, b vetypes.NonceCommitment) int {
	return cmp.Or(cmp.Compare(a.ShareIndex, b.ShareIndex), cmp.Compare(a.Seq, b.Seq))
}

func cmpShare(a, b vetypes.CombinedShare) int { return bytes.Compare(a.SessionId, b.SessionId) }
