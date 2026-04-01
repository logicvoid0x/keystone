package protocol

import (
	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// VoteExtHandler implements ExtendVote and VerifyVoteExtension (design §5.2). The
// sidecars (price, Bitcoin watcher, FROST signer) arrive in later milestones; in M0
// every VE has no payloads.
type VoteExtHandler struct{}

// NewVoteExtHandler returns the M0 handler.
func NewVoteExtHandler() *VoteExtHandler { return &VoteExtHandler{} }

// ExtendVote returns VoteExtension{version: 1, height}. It never returns an error.
func (h *VoteExtHandler) ExtendVote(_ sdk.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	ve := vetypes.VoteExtension{Version: VEVersion, Height: req.Height}
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
