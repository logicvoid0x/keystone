package app

import (
	cmttypes "github.com/cometbft/cometbft/types"
)

const (
	// BlockMaxBytes is consensus block.max_bytes (design §5.3).
	BlockMaxBytes = 4_194_304
	// BlockMaxGas is consensus block.max_gas (spec M0-T7).
	BlockMaxGas = 100_000_000
	// VoteExtensionsEnableHeight is 1: ExtendVote runs from height 1 and tx0 first
	// appears in block 2 (design §5.2).
	VoteExtensionsEnableHeight = 1
)

// DefaultConsensusParams are the genesis consensus params (design §10.4).
func DefaultConsensusParams() *cmttypes.ConsensusParams {
	cp := cmttypes.DefaultConsensusParams()
	cp.Block.MaxBytes = BlockMaxBytes
	cp.Block.MaxGas = BlockMaxGas
	cp.Validator.PubKeyTypes = []string{cmttypes.ABCIPubKeyTypeEd25519}
	cp.ABCI.VoteExtensionsEnableHeight = VoteExtensionsEnableHeight
	return cp
}
