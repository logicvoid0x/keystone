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

// VoteExtHandler implements ExtendVote and VerifyVoteExtension (design §5.2). The
// Bitcoin watcher and FROST signer arrive in later milestones.
type VoteExtHandler struct {
	price PriceSource
}

// NewVoteExtHandler returns a handler with no sidecars attached.
func NewVoteExtHandler() *VoteExtHandler { return &VoteExtHandler{} }

// SetPriceSource attaches the price sidecar. Call before the node starts.
func (h *VoteExtHandler) SetPriceSource(p PriceSource) { h.price = p }

// ExtendVote returns VoteExtension{version: 1, height, oracle from the price
// snapshot}. It never returns an error.
func (h *VoteExtHandler) ExtendVote(_ sdk.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	ve := vetypes.VoteExtension{Version: VEVersion, Height: req.Height}
	if h.price != nil {
		if p, ok := h.price.Snapshot(); ok && p != nil {
			ve.Oracle = p
		}
	}
	Canonicalize(&ve)
	bz, err := ve.Marshal()
	if err != nil || len(bz) > MaxVEBytes {
		return &abci.ResponseExtendVote{VoteExtension: MinimalVE(req.Height)}, nil
	}
	return &abci.ResponseExtendVote{VoteExtension: bz}, nil
}

// VerifyVoteExtension checks structure only: size, decoding, version, height, canonical
// order and counts. Content is judged during aggregation.
func (h *VoteExtHandler) VerifyVoteExtension(_ sdk.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	if _, err := DecodeVE(req.VoteExtension, req.Height); err != nil {
		return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_REJECT}, nil
	}
	return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_ACCEPT}, nil
}
