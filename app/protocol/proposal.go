package protocol

import (
	abci "github.com/cometbft/cometbft/abci/types"

	"cosmossdk.io/log/v2"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// ProposalHandler implements PrepareProposal and ProcessProposal (design §5.7, §5.8).
// User transactions are selected and checked by the SDK default handlers passed in.
type ProposalHandler struct {
	schedule       Schedule
	logger         log.Logger
	valStore       ValidatorStore
	prepareUserTxs sdk.PrepareProposalHandler
	processUserTxs sdk.ProcessProposalHandler
	onReject       func(reason string)
}

// NewProposalHandler wires the handler. prepareUserTxs and processUserTxs are the SDK
// default proposal handlers (baseapp.NewDefaultProposalHandler).
func NewProposalHandler(logger log.Logger, vs ValidatorStore, prepareUserTxs sdk.PrepareProposalHandler,
	processUserTxs sdk.ProcessProposalHandler,
) *ProposalHandler {
	return &ProposalHandler{logger: logger, valStore: vs, prepareUserTxs: prepareUserTxs, processUserTxs: processUserTxs}
}

// VEActive reports whether block `height` carries tx0: vote extensions are enabled and
// height is above the enable height, so the previous height produced VEs.
func VEActive(ctx sdk.Context, height int64) bool {
	cp := ctx.ConsensusParams()
	if cp.Abci == nil || cp.Abci.VoteExtensionsEnableHeight == 0 {
		return false
	}
	return height > cp.Abci.VoteExtensionsEnableHeight
}

// PrepareProposal puts tx0 (LocalLastCommit, unchanged) first when VEs are active, then
// user transactions within the remaining byte budget. Magic-prefixed mempool txs are
// dropped. It returns an error only if the SDK user-tx selection does.
func (h *ProposalHandler) PrepareProposal(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	if !VEActive(ctx, req.Height) {
		txs, err := h.userTxs(ctx, req, req.MaxTxBytes)
		if err != nil {
			return nil, err
		}
		return &abci.ResponsePrepareProposal{Txs: txs}, nil
	}

	ext := req.LocalLastCommit
	_, verified, total := VerifyVotes(ctx, h.valStore, ctx.ChainID(), req.Height-1, ext, h.schedule.at(ctx, req.Height-1))
	if !HasQuorum(verified, total) {
		// Should not happen: CometBFT already verified these signatures. Propose anyway;
		// peers reject and the next round retries.
		h.logger.Error("verified VE power not above 2/3", "height", req.Height, "verified", verified, "total", total)
	}
	inj := EncodeInjected(ext)
	txs, err := h.userTxs(ctx, req, req.MaxTxBytes-int64(len(inj)))
	if err != nil {
		return nil, err
	}
	return &abci.ResponsePrepareProposal{Txs: append([][]byte{inj}, txs...)}, nil
}

// userTxs keeps mempool order, drops magic-prefixed txs and stays within maxBytes.
func (h *ProposalHandler) userTxs(ctx sdk.Context, req *abci.RequestPrepareProposal, maxBytes int64) ([][]byte, error) {
	if maxBytes <= 0 {
		return nil, nil
	}
	sub := *req
	sub.Txs = withoutMagic(req.Txs)
	sub.MaxTxBytes = maxBytes
	res, err := h.prepareUserTxs(ctx, &sub)
	if err != nil {
		return nil, err
	}
	out := withoutMagic(res.Txs) // the app-side mempool could in principle return one
	var size int64
	for i, tx := range out {
		size += int64(len(tx))
		if size > maxBytes {
			return out[:i], nil
		}
	}
	return out, nil
}

func withoutMagic(txs [][]byte) [][]byte {
	out := make([][]byte, 0, len(txs))
	for _, tx := range txs {
		if !HasMagic(tx) {
			out = append(out, tx)
		}
	}
	return out
}

// Reject reasons reported to the observer (metric process_proposal_rejects{reason}).
const (
	RejectMagicBeforeVEs   = "magic_before_ves"
	RejectMissingTx0       = "missing_tx0"
	RejectExtraMagic       = "extra_magic"
	RejectBadTx0           = "undecodable_tx0"
	RejectLastCommit       = "last_commit_mismatch"
	RejectLowVerifiedPower = "verified_power"
	RejectUserTxs          = "user_txs"
)

// SetRejectObserver installs a callback told why a proposal was rejected. It only
// observes; ProcessProposal's result does not depend on it.
func (h *ProposalHandler) SetRejectObserver(f func(reason string)) { h.onReject = f }

func (h *ProposalHandler) reject(reason string) (*abci.ResponseProcessProposal, error) {
	if h.onReject != nil {
		h.onReject(reason)
	}
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
}

func (h *ProposalHandler) userTxsResult(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	res, err := h.processUserTxs(ctx, req)
	if err == nil && res.Status == abci.ResponseProcessProposal_REJECT {
		return h.reject(RejectUserTxs)
	}
	return res, err
}

// ProcessProposal accepts a block only if tx0 is present exactly at index 0 (when VEs
// are active), decodes, matches the block's LastCommit, and carries verified VE power
// above 2/3; and the user txs pass the SDK default checks.
func (h *ProposalHandler) ProcessProposal(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	if !VEActive(ctx, req.Height) {
		for _, tx := range req.Txs {
			if HasMagic(tx) {
				return h.reject(RejectMagicBeforeVEs)
			}
		}
		return h.userTxsResult(ctx, req)
	}
	if len(req.Txs) == 0 || !HasMagic(req.Txs[0]) {
		return h.reject(RejectMissingTx0)
	}
	for _, tx := range req.Txs[1:] {
		if HasMagic(tx) {
			return h.reject(RejectExtraMagic)
		}
	}
	ext, ok := DecodeInjected(req.Txs[0])
	if !ok {
		return h.reject(RejectBadTx0)
	}
	if !MatchesLastCommit(ext, req.ProposedLastCommit) {
		return h.reject(RejectLastCommit)
	}
	_, verified, total := VerifyVotes(ctx, h.valStore, ctx.ChainID(), req.Height-1, ext, h.schedule.at(ctx, req.Height-1))
	if !HasQuorum(verified, total) {
		return h.reject(RejectLowVerifiedPower)
	}
	sub := *req
	sub.Txs = req.Txs[1:]
	return h.userTxsResult(ctx, &sub)
}

// SetSchedule sets the VE version schedule (design §17).
func (h *ProposalHandler) SetSchedule(s Schedule) { h.schedule = s }
