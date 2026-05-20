//go:build !byzantine

package app

import sdk "github.com/cosmos/cosmos-sdk/types"

// ByzantineEnabled: release builds always propose honestly.
const ByzantineEnabled = false

func wrapPrepare(h sdk.PrepareProposalHandler) sdk.PrepareProposalHandler { return h }
