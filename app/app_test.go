package app_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/logicvoid0x/keystone/app"
	"github.com/logicvoid0x/keystone/testutil/apptest"
	admintypes "github.com/logicvoid0x/keystone/x/admin/types"
)

func TestInitChainReturnsStakingValidatorSet(t *testing.T) {
	c := apptest.New(t, 4)
	// x/staking InitGenesis returns the bonded genesis validators (design §10.4).
	require.Len(t, c.InitValidators, 4)
	for _, u := range c.InitValidators {
		require.Equal(t, int64(1), u.Power)
	}
	c.Finalize(nil, abci.CommitInfo{})
	ops, err := c.App.StakingKeeper.GetBondedValidatorsByPower(c.Context())
	require.NoError(t, err)
	require.Len(t, ops, 4)
}

func TestModuleAccountsExist(t *testing.T) {
	c := apptest.New(t, 1)
	c.Finalize(nil, abci.CommitInfo{})
	ctx := c.Context()
	for name, perms := range map[string][]string{
		app.StablecoinModuleAccount:    {authtypes.Minter, authtypes.Burner},
		app.SurplusModuleAccount:       nil,
		app.LiquidationModuleAccount:   {authtypes.Burner},
		stakingtypes.BondedPoolName:    {authtypes.Burner, authtypes.Staking},
		stakingtypes.NotBondedPoolName: {authtypes.Burner, authtypes.Staking},
		minttypes.ModuleName:           {authtypes.Minter},
		distrtypes.ModuleName:          nil,
	} {
		acc := c.App.AccountKeeper.GetModuleAccount(ctx, name)
		require.NotNil(t, acc, name)
		for _, p := range perms {
			require.True(t, acc.HasPermission(p), "%s has %s", name, p)
		}
	}
}

func TestAnteLimitsTxsPerSignerPerBlock(t *testing.T) {
	c := apptest.New(t, 1)
	c.Finalize(nil, abci.CommitInfo{})
	from, to := c.Users[0], c.Users[1]
	var txs [][]byte
	for i := 0; i < app.MaxTxPerAccountPerBlock+1; i++ {
		txs = append(txs, c.SignTx(from.Key, banktypes.NewMsgSend(from.Addr, to.Addr, sdk.NewCoins(sdk.NewInt64Coin(app.TestDenom, 1)))))
	}
	res := c.Finalize(txs, abci.CommitInfo{})
	require.Len(t, res.TxResults, len(txs))
	for i := 0; i < app.MaxTxPerAccountPerBlock; i++ {
		require.Equal(t, uint32(0), res.TxResults[i].Code, "tx %d: %s", i, res.TxResults[i].Log)
	}
	last := res.TxResults[app.MaxTxPerAccountPerBlock]
	require.Equal(t, app.ErrTooManyTxs.ABCICode(), last.Code, last.Log)

	// The counter resets in the next block.
	res = c.Finalize([][]byte{c.SignTx(from.Key, banktypes.NewMsgSend(from.Addr, to.Addr, sdk.NewCoins(sdk.NewInt64Coin(app.TestDenom, 1))))}, abci.CommitInfo{})
	require.Equal(t, uint32(0), res.TxResults[0].Code, res.TxResults[0].Log)
}

func TestUpgradeAuthorityIsAdmin(t *testing.T) {
	c := apptest.New(t, 1)
	c.Finalize(nil, abci.CommitInfo{})
	plan := upgradetypes.Plan{Name: "v2", Height: 1000}

	stranger := c.Users[0]
	res := c.Finalize([][]byte{c.SignTx(stranger.Key, &upgradetypes.MsgSoftwareUpgrade{Authority: stranger.Addr.String(), Plan: plan})}, abci.CommitInfo{})
	require.NotEqual(t, uint32(0), res.TxResults[0].Code)
	require.Contains(t, res.TxResults[0].Log, admintypes.ErrUnauthorized.Error())

	res = c.Finalize([][]byte{c.SignTx(c.Admin.Key, &upgradetypes.MsgSoftwareUpgrade{Authority: c.Admin.Addr.String(), Plan: plan})}, abci.CommitInfo{})
	require.Equal(t, uint32(0), res.TxResults[0].Code, res.TxResults[0].Log)
	got, err := c.App.UpgradeKeeper.GetUpgradePlan(c.Context())
	require.NoError(t, err)
	require.Equal(t, "v2", got.Name)
}

func TestBlockExecutorMustBeSequential(t *testing.T) {
	require.Panics(t, func() { app.New(nopLogger(), memDB(), false, appOpts{"block-executor": "block-stm"}) })
	require.NotPanics(t, func() { app.New(nopLogger(), memDB(), false, appOpts{"block-executor": "sequential"}) })
}
