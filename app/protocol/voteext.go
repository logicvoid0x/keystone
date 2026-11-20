package protocol

import (
	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
)

// PriceSource is the price sidecar's snapshot (design §4.3). Snapshot must not block
// on network I/O.
type PriceSource interface {
	Snapshot() (*vetypes.OraclePayload, bool)
}

// FrostSource is the FROST signer (design §9.11). Payload may do local file I/O (the
// nonce store fsyncs) but no network I/O.
type FrostSource interface {
	Payload(height int64) (*vetypes.FrostPayload, error)
}

// BitcoinSource is the Bitcoin watcher's snapshot (design §9.7). Snapshot must not
// block on network I/O.
type BitcoinSource interface {
	Snapshot() (*vetypes.BitcoinPayload, bool)
}

// VoteExtHandler implements ExtendVote and VerifyVoteExtension (design §5.2).
type VoteExtHandler struct {
	schedule Schedule
	price    PriceSource
	bitcoin  BitcoinSource
	frost    FrostSource
	onFrost  func(error)
}

// NewVoteExtHandler returns a handler with no sidecars attached.
func NewVoteExtHandler() *VoteExtHandler { return &VoteExtHandler{} }

// SetPriceSource attaches the price sidecar. Call before the node starts.
func (h *VoteExtHandler) SetPriceSource(p PriceSource) { h.price = p }

// SetBitcoinSource attaches the Bitcoin watcher. Call before the node starts.
func (h *VoteExtHandler) SetBitcoinSource(b BitcoinSource) { h.bitcoin = b }

// SetSchedule sets the VE version schedule (design §17).
func (h *VoteExtHandler) SetSchedule(s Schedule) { h.schedule = s }

// SetFrostSource attaches the FROST signer; errors are passed to onErr (may be nil).
// Call before the node starts.
func (h *VoteExtHandler) SetFrostSource(f FrostSource, onErr func(error)) {
	h.frost, h.onFrost = f, onErr
}

// ExtendVote returns VoteExtension{version: 1, height, oracle from the price snapshot,
// bitcoin from the watcher snapshot, frost from the signer}. It never returns an error;
// a signer error leaves the FROST payload out.
func (h *VoteExtHandler) ExtendVote(ctx sdk.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	version := h.schedule.at(ctx, req.Height)
	ve := vetypes.VoteExtension{Version: version, Height: req.Height}
	if h.price != nil {
		if p, ok := h.price.Snapshot(); ok && p != nil {
			ve.Oracle = p
		}
	}
	if h.bitcoin != nil {
		if b, ok := h.bitcoin.Snapshot(); ok && b != nil {
			ve.Bitcoin = b
		}
	}
	if h.frost != nil {
		f, err := h.frost.Payload(req.Height)
		switch {
		case err != nil:
			if h.onFrost != nil {
				h.onFrost(err)
			}
		case f != nil && (len(f.Commitments) > 0 || len(f.Shares) > 0 || f.RevokeAllNonces):
			ve.Frost = f
		}
	}
	if bz, ok := encodeVE(&ve); ok {
		return &abci.ResponseExtendVote{VoteExtension: bz}, nil
	}
	// A Bitcoin or FROST payload that breaks a limit must not cost the node its price
	// vote: drop Bitcoin first, then FROST.
	ve.Bitcoin = nil
	if bz, ok := encodeVE(&ve); ok {
		return &abci.ResponseExtendVote{VoteExtension: bz}, nil
	}
	ve.Frost = nil
	if bz, ok := encodeVE(&ve); ok {
		return &abci.ResponseExtendVote{VoteExtension: bz}, nil
	}
	return &abci.ResponseExtendVote{VoteExtension: MinimalVEAt(req.Height, version)}, nil
}

// encodeVE canonicalizes and encodes ve if it passes the checks VerifyVoteExtension
// applies (canonical order, counts, size).
func encodeVE(ve *vetypes.VoteExtension) ([]byte, bool) {
	Canonicalize(ve)
	if !IsCanonical(ve) || !WithinCounts(ve) {
		return nil, false
	}
	bz, err := ve.Marshal()
	if err != nil || len(bz) > MaxVEBytes {
		return nil, false
	}
	return bz, true
}

// VerifyVoteExtension checks structure only: size, decoding, version, height, canonical
// order and counts. Content is judged during aggregation.
func (h *VoteExtHandler) VerifyVoteExtension(ctx sdk.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	if _, err := DecodeVEAt(req.VoteExtension, req.Height, h.schedule.at(ctx, req.Height)); err != nil {
		return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_REJECT}, nil
	}
	return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_ACCEPT}, nil
}
