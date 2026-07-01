package apptest

import (
	"math/rand"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/testutil/devdealer"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
)

// DealCustody puts a dealer epoch for operators into gs (seeded, so every test run
// derives the same addresses), with the localnet custody params: regtest, k = 1.
func DealCustody(cdc codec.JSONCodec, gs app.GenesisState, operators []sdk.AccAddress) error {
	var cg custodytypes.GenesisState
	if err := cdc.UnmarshalJSON(gs[custodytypes.ModuleName], &cg); err != nil {
		return err
	}
	d, err := devdealer.Deal(rand.New(rand.NewSource(7)), operators)
	if err != nil {
		return err
	}
	d.Apply(&cg)
	cg.Params.BitcoinNetwork = custodytypes.NetworkRegtest
	cg.Params.ConfirmDepth = 1
	cg.Params.MembershipDelayBlocks = 100
	bz, err := cdc.MarshalJSON(&cg)
	if err != nil {
		return err
	}
	gs[custodytypes.ModuleName] = bz
	return nil
}

func operatorAddrs(ops []Operator) []sdk.AccAddress {
	out := make([]sdk.AccAddress, len(ops))
	for i, op := range ops {
		out[i] = op.Addr
	}
	return out
}

// DepositScript returns vault id's deposit script from committed state.
func (c *Chain) DepositScript(id uint64) []byte {
	c.T.Helper()
	_, script, _, err := c.App.CustodyKeeper.DepositAddress(c.Context(), id)
	require.NoError(c.T, err)
	return script
}

// SendBTC pays sat to vault id's deposit address on the fake chain and mines one
// block (k = 1). The deposit is credited two Keystone blocks later: the watcher puts
// it in the next VE, and the block after that applies it.
func (c *Chain) SendBTC(id uint64, sat int64) chainhash.Hash {
	c.T.Helper()
	txid := c.Bitcoin.Send(c.DepositScript(id), sat)
	c.Bitcoin.Mine(1)
	return txid
}

// Fund deposits sat to vault id and runs blocks until it is accepted (at most 4).
func (c *Chain) Fund(id uint64, sat int64) {
	c.T.Helper()
	txid := c.SendBTC(id, sat)
	for i := 0; i < 4; i++ {
		c.NextBlock(nil)
		if has, err := c.App.CustodyKeeper.Deposits.Has(c.Context(), collections.Join(txid[:], uint32(0))); err == nil && has {
			return
		}
	}
	c.T.Fatalf("deposit of %d sat to vault %d not accepted", sat, id)
}

// Collateral returns vault id's collateral from committed state.
func (c *Chain) Collateral(id uint64) sdkmath.Int {
	c.T.Helper()
	v, err := c.App.VaultKeeper.GetVault(c.Context(), id)
	require.NoError(c.T, err)
	return v.CollateralSat
}
