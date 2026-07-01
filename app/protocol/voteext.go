package protocol

import (
	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// PriceSource is the price sidecar's snapshot (design §4.3). Snapshot must not block
// on network I/O.
type PriceSource interface {
	Snapshot() (*vetypes.OraclePayload, bool)
}

// BitcoinSource is the Bitcoin watcher's snapshot (design §9.7). Snapshot must not
// block on network I/O.
type BitcoinSource interface {
	Snapshot() (*vetypes.BitcoinPayload, bool)
}

// VoteExtHandler implements ExtendVote and VerifyVoteExtension (design §5.2). The
// FROST signer arrives in M4a.
type VoteExtHandler struct {
	price   PriceSource
	bitcoin BitcoinSource
}

// NewVoteExtHandler returns a handler with no sidecars attached.
func NewVoteExtHandler() *VoteExtHandler { return &VoteExtHandler{} }

// SetPriceSource attaches the price sidecar. Call before the node starts.
func (h *VoteExtHandler) SetPriceSource(p PriceSource) { h.price = p }

// SetBitcoinSource attaches the Bitcoin watcher. Call before the node starts.
func (h *VoteExtHandler) SetBitcoinSource(b BitcoinSource) { h.bitcoin = b }

// ExtendVote returns VoteExtension{version: 1, height, oracle from the price snapshot,
// bitcoin from the watcher snapshot}. It never returns an error.
func (h *VoteExtHandler) ExtendVote(_ sdk.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	ve := vetypes.VoteExtension{Version: VEVersion, Height: req.Height}
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
	if bz, ok := encodeVE(&ve); ok {
		return &abci.ResponseExtendVote{VoteExtension: bz}, nil
	}
	// A Bitcoin payload that breaks a limit must not cost the node its price vote.
	ve.Bitcoin = nil
	if bz, ok := encodeVE(&ve); ok {
		return &abci.ResponseExtendVote{VoteExtension: bz}, nil
	}
	return &abci.ResponseExtendVote{VoteExtension: MinimalVE(req.Height)}, nil
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
func (h *VoteExtHandler) VerifyVoteExtension(_ sdk.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	if _, err := DecodeVE(req.VoteExtension, req.Height); err != nil {
		return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_REJECT}, nil
	}
	return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_ACCEPT}, nil
}
