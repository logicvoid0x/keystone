//go:build byzantine

package app

import (
	"os"

	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol"
)

// ByzantineEnabled: this binary can misbehave as a proposer (adversarial tests only,
// design §15; never in release builds).
const ByzantineEnabled = true

// Byzantine proposer modes, chosen by the KEYSTONE_BYZ environment variable.
const (
	ByzDropTx0       = "drop_tx0"
	ByzExtraMagic    = "extra_magic"
	ByzCorruptOneVE  = "corrupt_one_ve"
	ByzCorruptTwoVEs = "corrupt_two_ve"
)

// ByzantineEnv is the environment variable selecting the mode.
const ByzantineEnv = "KEYSTONE_BYZ"

// wrapPrepare makes PrepareProposal misbehave according to KEYSTONE_BYZ (read on every
// call). An unknown or empty mode proposes honestly.
func wrapPrepare(h sdk.PrepareProposalHandler) sdk.PrepareProposalHandler {
	return func(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
		res, err := h(ctx, req)
		if err != nil || len(res.Txs) == 0 || !protocol.HasMagic(res.Txs[0]) {
			return res, err
		}
		mode := os.Getenv(ByzantineEnv)
		txs := res.Txs
		switch mode {
		case ByzDropTx0:
			txs = txs[1:]
		case ByzExtraMagic:
			txs = append(txs, append([]byte("KSTN\x02"), []byte("forged liquidation list: vaults 1,2,3")...))
		case ByzCorruptOneVE, ByzCorruptTwoVEs:
			ext, ok := protocol.DecodeInjected(txs[0])
			n := 1
			if mode == ByzCorruptTwoVEs {
				n = 2
			}
			for i := 0; ok && i < len(ext.Votes) && n > 0; i++ {
				if len(ext.Votes[i].ExtensionSignature) > 0 {
					sig := append([]byte(nil), ext.Votes[i].ExtensionSignature...)
					sig[0] ^= 0xFF
					ext.Votes[i].ExtensionSignature = sig
					n--
				}
			}
			if ok {
				txs = append([][]byte{protocol.EncodeInjected(ext)}, txs[1:]...)
			}
		default:
			return res, nil
		}
		ctx.Logger().Error("BYZANTINE proposer", "mode", mode, "height", req.Height)
		return &abci.ResponsePrepareProposal{Txs: txs}, nil
	}
}
