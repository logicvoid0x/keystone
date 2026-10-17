package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/OWNER/keystone/testutil/fakebank"
	custodykeeper "github.com/OWNER/keystone/x/custody/keeper"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
	stablecoinkeeper "github.com/OWNER/keystone/x/stablecoin/keeper"
	stablecointypes "github.com/OWNER/keystone/x/stablecoin/types"
	"github.com/OWNER/keystone/x/vault/keeper"
	"github.com/OWNER/keystone/x/vault/types"
)

var (
	adminAddr = sdk.AccAddress(bytes.Repeat([]byte{0xAD}, 20))
	admin     = adminAddr.String()
	stranger  = sdk.AccAddress(bytes.Repeat([]byte{0x05}, 20)).String()
	alice     = sdk.AccAddress(bytes.Repeat([]byte{0xA1}, 20))
	// wpkh is a P2WPKH withdrawal script.
	wpkh        = append([]byte{0x00, 0x14}, bytes.Repeat([]byte{0x77}, 20)...)
	bob         = sdk.AccAddress(bytes.Repeat([]byte{0xB0}, 20))
	errNotAdmin = errors.New("not admin")
)

const usd = 1_000_000

type fakeAdmin struct{}

func (fakeAdmin) CheckAdmin(_ context.Context, signer string) error {
	if signer != admin {
		return errNotAdmin
	}
	return nil
}

// fakeOracle has a settable spot, TWAP and status.
type fakeOracle struct {
	spot, twap sdkmath.Int
	status     oracletypes.PriceStatus
}

func (o *fakeOracle) GetPriceState(context.Context) (oracletypes.PriceState, error) {
	return oracletypes.PriceState{SpotMicroUsd: o.spot, TwapMicroUsd: o.twap, Status: o.status}, nil
}

func (o *fakeOracle) CollateralPrice(context.Context) (sdkmath.Int, bool, error) {
	return sdkmath.MinInt(o.spot, o.twap), o.status != oracletypes.PRICE_STATUS_STALE, nil
}

type fixture struct {
	ctx     sdk.Context
	k       *keeper.Keeper
	sc      *stablecoinkeeper.Keeper
	cust    *custodykeeper.Keeper
	bank    *fakebank.Bank
	oracle  *fakeOracle
	genesis time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	keys := storetypes.NewKVStoreKeys(types.StoreKey, stablecointypes.StoreKey, custodytypes.StoreKey)
	genesis := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := testutil.DefaultContextWithKeys(keys, nil, nil).WithBlockTime(genesis).WithBlockHeight(1).
		WithEventManager(sdk.NewEventManager())
	cdc := moduletestutil.MakeTestEncodingConfig().Codec
	bank := fakebank.New(map[string][]string{
		stablecointypes.ModuleName: {authtypes.Minter, authtypes.Burner},
	})
	sc := stablecoinkeeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[stablecointypes.StoreKey]), fakeAdmin{}, bank)
	cust := custodykeeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[custodytypes.StoreKey]), fakeAdmin{}, nil)
	or := &fakeOracle{spot: sdkmath.NewInt(100_000 * usd), twap: sdkmath.NewInt(100_000 * usd), status: oracletypes.PRICE_STATUS_ACTIVE}
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), fakeAdmin{}, or, sc, cust)
	require.NoError(t, sc.InitGenesis(ctx, stablecointypes.DefaultGenesis()))
	require.NoError(t, cust.InitGenesis(ctx, custodytypes.DefaultGenesis()))
	require.NoError(t, k.InitGenesis(ctx, types.DefaultGenesis()))
	return &fixture{ctx: ctx, k: k, sc: sc, cust: cust, bank: bank, oracle: or, genesis: genesis}
}

func n(v int64) sdkmath.Int { return sdkmath.NewInt(v) }

func kusd(v int64) sdkmath.Int { return n(v * 1_000_000) }

// open opens a vault for owner and dev-credits sat.
func (f *fixture) open(t *testing.T, owner sdk.AccAddress, sat int64) uint64 {
	t.Helper()
	id, _, err := f.k.OpenVault(f.ctx, &types.MsgOpenVault{Owner: owner.String()})
	require.NoError(t, err)
	if sat > 0 {
		_, err := f.k.CreditCollateral(f.ctx, id, n(sat))
		require.NoError(t, err)
	}
	return id
}

func (f *fixture) mint(owner sdk.AccAddress, id uint64, amt sdkmath.Int) error {
	return f.k.Mint(f.ctx, &types.MsgMint{Owner: owner.String(), VaultId: id, Amount: amt})
}

func (f *fixture) vault(t *testing.T, id uint64) types.Vault {
	v, err := f.k.GetVault(f.ctx, id)
	require.NoError(t, err)
	return v
}

func (f *fixture) indexed(t *testing.T, id uint64) bool {
	found := false
	require.NoError(t, f.k.LiqIndex.Walk(f.ctx, nil, func(key collections.Pair[[]byte, uint64], _ bool) (bool, error) {
		if key.K2() == id {
			found = true
		}
		return false, nil
	}))
	return found
}

func TestOpenAndCreditCollateral(t *testing.T) {
	f := setup(t)
	id1 := f.open(t, alice, 0)
	id2 := f.open(t, bob, 0)
	require.Equal(t, uint64(1), id1)
	require.Equal(t, uint64(2), id2)
	require.Equal(t, types.VAULT_STATUS_OPEN, f.vault(t, id1).Status)

	c, err := f.k.CreditCollateral(f.ctx, id1, n(50_000_000))
	require.NoError(t, err)
	require.Equal(t, n(50_000_000), c)
	v := f.vault(t, id1)
	require.Equal(t, n(50_000_000), v.CollateralSat)
	require.Equal(t, types.VAULT_STATUS_ACTIVE, v.Status)
	cr, _ := f.cust.GetCredited(f.ctx)
	require.Equal(t, n(50_000_000), cr)

	// Over the 10 BTC cap: the excess goes to the owner's claimable balance.
	c, err = f.k.CreditCollateral(f.ctx, id2, n(1_000_000_000))
	require.NoError(t, err)
	require.Equal(t, n(950_000_000), c)
	require.Equal(t, n(950_000_000), f.vault(t, id2).CollateralSat)
	cl, _ := f.cust.GetClaimable(f.ctx, bob)
	require.Equal(t, n(50_000_000), cl)

	_, err = f.k.GetVault(f.ctx, 99)
	require.ErrorIs(t, err, types.ErrVaultNotFound)
}

func TestMintRules(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fixture, id uint64)
		owner sdk.AccAddress
		amt   sdkmath.Int
		err   error
	}{
		{"valid", nil, alice, kusd(30_000), nil},
		{"max at MCR", nil, alice, n(33_333_333_333), nil},
		{"above MCR", nil, alice, n(33_333_333_334), types.ErrBelowMCR},
		{"not owner", nil, bob, kusd(1_000), types.ErrNotOwner},
		{"below dust", nil, alice, kusd(99), types.ErrDust},
		{"price volatile", func(f *fixture, _ uint64) { f.oracle.status = oracletypes.PRICE_STATUS_VOLATILE }, alice, kusd(1_000), types.ErrPriceStatus},
		{"price stale", func(f *fixture, _ uint64) { f.oracle.status = oracletypes.PRICE_STATUS_STALE }, alice, kusd(1_000), types.ErrPriceStatus},
		{"uses min(spot, TWAP)", func(f *fixture, _ uint64) { f.oracle.twap = n(60_000 * usd) }, alice, kusd(30_000), types.ErrBelowMCR},
		{"mints paused", func(f *fixture, _ uint64) { _ = f.sc.MintsPaused.Set(f.ctx, true) }, alice, kusd(1_000), types.ErrMintsPaused},
		{"ceiling", func(f *fixture, _ uint64) {
			p := types.DefaultParams()
			p.GlobalDebtCeiling = kusd(20_000)
			_ = f.k.Params.Set(f.ctx, p)
		}, alice, kusd(20_001), types.ErrCeiling},
		{"no collateral (OPEN)", func(f *fixture, _ uint64) {}, alice, kusd(1_000), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			id := f.open(t, alice, 50_000_000)
			if tc.name == "no collateral (OPEN)" {
				id = f.open(t, alice, 0)
				tc.err = types.ErrVaultStatus
			}
			if tc.setup != nil {
				tc.setup(f, id)
			}
			err := f.mint(tc.owner, id, tc.amt)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.amt, f.bank.Balance(alice, stablecointypes.Denom))
			require.Equal(t, tc.amt, f.vault(t, id).Art, "rate = 1 RAY: art = amount")
			require.True(t, f.indexed(t, id))
		})
	}
}

func TestRepay(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 50_000_000)
	require.NoError(t, f.mint(alice, id, kusd(30_000)))

	// Partial, leaving dust: rejected.
	_, err := f.k.Repay(f.ctx, &types.MsgRepay{Sender: alice.String(), VaultId: id, Amount: kusd(29_950)})
	require.ErrorIs(t, err, types.ErrDust)

	// Partial by a third party who holds KUSD.
	require.NoError(t, f.bank.SendCoinsFromAccountToModule(f.ctx, alice, stablecointypes.ModuleName, sdk.NewCoins(sdk.NewCoin(stablecointypes.Denom, kusd(1_000)))))
	require.NoError(t, f.bank.SendCoinsFromModuleToAccount(f.ctx, stablecointypes.ModuleName, bob, sdk.NewCoins(sdk.NewCoin(stablecointypes.Denom, kusd(1_000)))))
	r, err := f.k.Repay(f.ctx, &types.MsgRepay{Sender: bob.String(), VaultId: id, Amount: kusd(1_000)})
	require.NoError(t, err)
	require.Equal(t, kusd(1_000), r)
	require.Equal(t, kusd(29_000), f.vault(t, id).Art)

	// Overpaying burns exactly the debt and zeroes art.
	r, err = f.k.Repay(f.ctx, &types.MsgRepay{Sender: alice.String(), VaultId: id, Amount: kusd(1_000_000)})
	require.NoError(t, err)
	require.Equal(t, kusd(29_000), r)
	require.True(t, f.vault(t, id).Art.IsZero())
	require.False(t, f.indexed(t, id), "art = 0 is not indexed")
	require.True(t, f.sc.Supply(f.ctx).IsZero())

	_, err = f.k.Repay(f.ctx, &types.MsgRepay{Sender: alice.String(), VaultId: id, Amount: kusd(1)})
	require.ErrorIs(t, err, types.ErrInvalidAmount, "no debt")
}

func TestWithdrawAndClose(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 50_000_000)
	require.NoError(t, f.mint(alice, id, kusd(30_000)))
	w := func(owner sdk.AccAddress, sat int64) error {
		_, err := f.k.WithdrawCollateral(f.ctx, &types.MsgWithdrawCollateral{Owner: owner.String(), VaultId: id, AmountSat: n(sat), RecipientScript: wpkh})
		return err
	}
	require.ErrorIs(t, w(alice, 9_999), custodytypes.ErrInvalidAmount, "below MinWithdrawSat")
	require.ErrorIs(t, w(alice, 6_000_000), types.ErrBelowMCR)
	require.ErrorIs(t, w(bob, 10_000), types.ErrNotOwner)
	_, err := f.k.WithdrawCollateral(f.ctx, &types.MsgWithdrawCollateral{Owner: alice.String(), VaultId: id, AmountSat: n(10_000), RecipientScript: []byte{0x6a}})
	require.ErrorIs(t, err, custodytypes.ErrInvalidScript)
	f.oracle.status = oracletypes.PRICE_STATUS_STALE
	require.ErrorIs(t, w(alice, 10_000), types.ErrPriceStatus)
	f.oracle.status = oracletypes.PRICE_STATUS_ACTIVE

	require.NoError(t, w(alice, 4_000_000)) // CR stays ≥ 150%
	require.Equal(t, n(46_000_000), f.vault(t, id).CollateralSat)
	cl, _ := f.cust.GetClaimable(f.ctx, alice)
	require.True(t, cl.IsZero(), "the withdrawal is queued, not made claimable")
	cr, _ := f.cust.GetCredited(f.ctx)
	require.Equal(t, n(46_000_000), cr)
	req, err := f.cust.Requests.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, custodytypes.WithdrawalRequest{Id: 1, Owner: alice.String(), RecipientScript: wpkh, AmountSat: 4_000_000,
		Status: custodytypes.REQUEST_STATUS_QUEUED, CreatedHeight: f.ctx.BlockHeight()}, req)

	_, err = f.k.CloseVault(f.ctx, &types.MsgCloseVault{Owner: alice.String(), VaultId: id, RecipientScript: wpkh})
	require.ErrorIs(t, err, types.ErrHasDebt)
	_, err = f.k.Repay(f.ctx, &types.MsgRepay{Sender: alice.String(), VaultId: id, Amount: kusd(30_000)})
	require.NoError(t, err)
	_, err = f.k.CloseVault(f.ctx, &types.MsgCloseVault{Owner: alice.String(), VaultId: id, RecipientScript: []byte{0x6a}})
	require.ErrorIs(t, err, custodytypes.ErrInvalidScript)
	rid, err := f.k.CloseVault(f.ctx, &types.MsgCloseVault{Owner: alice.String(), VaultId: id, RecipientScript: wpkh})
	require.NoError(t, err)
	require.Equal(t, uint64(2), rid)
	v := f.vault(t, id)
	require.Equal(t, types.VAULT_STATUS_CLOSED, v.Status)
	require.True(t, v.CollateralSat.IsZero())
	req, err = f.cust.Requests.Get(f.ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(46_000_000), req.AmountSat)
	q, err := f.cust.QueuedSat(f.ctx)
	require.NoError(t, err)
	require.Equal(t, n(50_000_000), q)
	cr, _ = f.cust.GetCredited(f.ctx)
	require.True(t, cr.IsZero())
	require.ErrorIs(t, f.mint(alice, id, kusd(100)), types.ErrVaultStatus)
}

// A vault holding less than MinWithdrawSat closes into the owner's claimable balance.
func TestCloseDustVault(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 9_000)
	rid, err := f.k.CloseVault(f.ctx, &types.MsgCloseVault{Owner: alice.String(), VaultId: id})
	require.NoError(t, err)
	require.Zero(t, rid)
	cl, _ := f.cust.GetClaimable(f.ctx, alice)
	require.Equal(t, n(9_000), cl)
}

func TestDepositClaim(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 0)
	require.NoError(t, f.cust.CreditClaimable(f.ctx, alice, n(2_000_000)))
	dc := func(sat int64) error {
		return f.k.DepositClaim(f.ctx, &types.MsgDepositClaim{Owner: alice.String(), VaultId: id, AmountSat: n(sat)})
	}
	require.ErrorIs(t, dc(3_000_000), custodytypes.ErrInsufficient)
	require.NoError(t, dc(2_000_000))
	require.Equal(t, n(2_000_000), f.vault(t, id).CollateralSat)
	cr, _ := f.cust.GetCredited(f.ctx)
	require.Equal(t, n(2_000_000), cr)

	// No headroom: rejected, claimable untouched.
	require.NoError(t, f.cust.CreditClaimable(f.ctx, alice, n(1)))
	p := custodytypes.DefaultParams()
	p.TreasuryCapSat = n(2_000_000)
	require.NoError(t, f.cust.Params.Set(f.ctx, p))
	require.ErrorIs(t, dc(1), types.ErrInvalidAmount)
	cl, _ := f.cust.GetClaimable(f.ctx, alice)
	require.Equal(t, n(1), cl)
}

// Required vectors: 30 days of fees on 30,000 KUSD.
func TestDripThirtyDays(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 50_000_000)
	require.NoError(t, f.mint(alice, id, kusd(30_000)))

	f.ctx = f.ctx.WithBlockTime(f.genesis.Add(30 * 24 * time.Hour))
	require.NoError(t, f.k.Drip(f.ctx, f.ctx.BlockTime()))
	rs, err := f.k.RateState.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, "1001628938483711657286055762", rs.Rate.String())
	debt, err := f.k.Debt(f.ctx, f.vault(t, id))
	require.NoError(t, err)
	require.Equal(t, "30048868155", debt.String())
	// Fee revenue floor(30e9 × Δrate / RAY) = 48,868,154 ukusd: 80% to surplus, the
	// stakers' 20% (floor 9,773,630) to the fee collector (design §7.7).
	require.Equal(t, "39094524", f.sc.SurplusBalance(f.ctx).String())

	// Liquidation price ≈ $78,127.06: liquidatable just below, not at.
	ok, err := f.k.IsLiquidatable(f.ctx, f.vault(t, id), n(78_127_057_203))
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = f.k.IsLiquidatable(f.ctx, f.vault(t, id), n(78_127_057_202))
	require.NoError(t, err)
	require.True(t, ok)

	// Same block again: no further accrual.
	require.NoError(t, f.k.Drip(f.ctx, f.ctx.BlockTime()))
	require.Equal(t, "39094524", f.sc.SurplusBalance(f.ctx).String())
}

func TestLiquidationIndexAndIterate(t *testing.T) {
	f := setup(t)
	a := f.open(t, alice, 50_000_000) // 30,000 KUSD: liq price ~$78,000
	b := f.open(t, bob, 100_000_000)  // 30,000 KUSD: liq price ~$39,000
	c := f.open(t, alice, 50_000_000) // 25,000 KUSD: liq price ~$65,000
	require.NoError(t, f.mint(alice, a, kusd(30_000)))
	require.NoError(t, f.mint(bob, b, kusd(30_000)))
	require.NoError(t, f.mint(alice, c, kusd(25_000)))

	var order []uint64
	require.NoError(t, f.k.LiqIndex.Walk(f.ctx, nil, func(key collections.Pair[[]byte, uint64], _ bool) (bool, error) {
		order = append(order, key.K2())
		return false, nil
	}))
	require.Equal(t, []uint64{a, c, b}, order, "worst ratio first")

	visit := func(price int64) []uint64 {
		var got []uint64
		require.NoError(t, f.k.IterateLiquidatable(f.ctx, n(price*usd), func(v types.Vault) bool {
			got = append(got, v.Id)
			return false
		}))
		return got
	}
	require.Empty(t, visit(100_000))
	require.Equal(t, []uint64{a}, visit(76_000))
	require.Equal(t, []uint64{a, c}, visit(60_000))
	require.Equal(t, []uint64{a, c, b}, visit(30_000))
	any, err := f.k.AnyLiquidatable(f.ctx, n(76_000*usd))
	require.NoError(t, err)
	require.True(t, any)

	// The callback can stop early.
	var first []uint64
	require.NoError(t, f.k.IterateLiquidatable(f.ctx, n(30_000*usd), func(v types.Vault) bool {
		first = append(first, v.Id)
		return true
	}))
	require.Equal(t, []uint64{a}, first)

	// Withdrawing collateral rewrites the key.
	_, err = f.k.WithdrawCollateral(f.ctx, &types.MsgWithdrawCollateral{Owner: bob.String(), VaultId: b, AmountSat: n(50_000_000), RecipientScript: wpkh})
	require.NoError(t, err)
	order = nil
	require.NoError(t, f.k.LiqIndex.Walk(f.ctx, nil, func(key collections.Pair[[]byte, uint64], _ bool) (bool, error) {
		order = append(order, key.K2())
		return false, nil
	}))
	require.Equal(t, []uint64{a, b, c}, order, "b now has 0.5 BTC for 30,000 KUSD; tie with a breaks on id")
}

func TestCloseForLiquidation(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 50_000_000)
	require.NoError(t, f.mint(alice, id, kusd(30_000)))
	_, err := f.k.CloseForLiquidation(f.ctx, id, n(50_000_001))
	require.ErrorIs(t, err, types.ErrInsufficient)

	debt, err := f.k.CloseForLiquidation(f.ctx, id, n(43_491_783))
	require.NoError(t, err)
	require.Equal(t, kusd(30_000), debt)
	v := f.vault(t, id)
	require.True(t, v.Art.IsZero())
	require.Equal(t, n(6_508_217), v.CollateralSat)
	require.Equal(t, types.VAULT_STATUS_LIQUIDATED, v.Status)
	require.False(t, f.indexed(t, id))
	ta, _ := f.k.TotalArt.Get(f.ctx)
	require.True(t, ta.IsZero())
	cr, _ := f.cust.GetCredited(f.ctx)
	require.Equal(t, n(6_508_217), cr)

	// A new deposit reactivates the vault (design §7.2).
	credited, err := f.k.CreditCollateral(f.ctx, id, n(1_000))
	require.NoError(t, err)
	require.Equal(t, n(1_000), credited)
	require.Equal(t, types.VAULT_STATUS_ACTIVE, f.vault(t, id).Status)

	short, err := f.k.DebitCollateral(f.ctx, id, n(10_000_000))
	require.NoError(t, err)
	require.Equal(t, n(10_000_000-6_509_217), short)
}

func TestQueries(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 50_000_000)
	require.NoError(t, f.mint(alice, id, kusd(30_000)))
	f.open(t, bob, 0)
	q := keeper.NewQuerier(f.k)

	v, err := q.Vault(f.ctx, &types.QueryVaultRequest{Id: id})
	require.NoError(t, err)
	require.Equal(t, kusd(30_000), v.Vault.Debt)
	_, err = q.Vault(f.ctx, &types.QueryVaultRequest{Id: 42})
	require.Error(t, err)

	byOwner, err := q.VaultsByOwner(f.ctx, &types.QueryVaultsByOwnerRequest{Owner: alice.String()})
	require.NoError(t, err)
	require.Len(t, byOwner.Vaults, 1)

	rs, err := q.RateState(f.ctx, &types.QueryRateStateRequest{})
	require.NoError(t, err)
	require.Equal(t, types.RAY, rs.RateState.Rate)
	require.Equal(t, kusd(30_000), rs.TotalArt)

	lq, err := q.LiquidationQueue(f.ctx, &types.QueryLiquidationQueueRequest{Limit: 10})
	require.NoError(t, err)
	require.Len(t, lq.Vaults, 1)
}

func TestGenesisAndParams(t *testing.T) {
	f := setup(t)
	gs, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultGenesis(), gs)
	require.ErrorIs(t, f.k.InitGenesis(f.ctx, &types.GenesisState{}), types.ErrInvalidParams)

	ms := keeper.NewMsgServerImpl(f.k)
	next := types.DefaultParams()
	next.McrBps++
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: stranger, Params: next})
	require.ErrorIs(t, err, errNotAdmin)
	_, err = ms.UpdateParams(f.ctx, &types.MsgUpdateParams{Admin: admin, Params: next})
	require.NoError(t, err)
	got, _ := f.k.GetParams(f.ctx)
	require.Equal(t, next, got)
}

// With an ACTIVE custody epoch, MsgOpenVault registers the vault's deposit script and
// returns its address (design §9.7 step 1).
func TestOpenVaultAddress(t *testing.T) {
	f := setup(t)
	f.ctx = f.ctx.WithChainID("keystone-local-1")
	_, pub := btcec.PrivKeyFromBytes([]byte{7})
	key := pub.SerializeCompressed()
	key[0] = 0x02
	cp := custodytypes.DefaultParams()
	cp.BitcoinNetwork = custodytypes.NetworkRegtest
	require.NoError(t, f.cust.Params.Set(f.ctx, cp))
	require.NoError(t, f.cust.Epochs.Set(f.ctx, 1, custodytypes.KeyEpoch{Number: 1, GroupKey: key, N: 1, T: 1, Status: custodytypes.EPOCH_STATUS_ACTIVE}))
	require.NoError(t, f.cust.ActiveEpoch.Set(f.ctx, 1))

	res, err := keeper.NewMsgServerImpl(f.k).OpenVault(f.ctx, &types.MsgOpenVault{Owner: alice.String()})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(res.Address, "bcrt1p"), res.Address)
	addr, script, _, err := f.cust.DepositAddress(f.ctx, res.VaultId)
	require.NoError(t, err)
	require.Equal(t, res.Address, addr)
	id, err := f.cust.ScriptVault.Get(f.ctx, script)
	require.NoError(t, err)
	require.Equal(t, res.VaultId, id)
	ev := f.ctx.EventManager().Events()
	require.Equal(t, res.Address, attr(ev, types.EventTypeVaultOpened, types.AttributeKeyAddress))

	owner, open, err := f.k.VaultOwner(f.ctx, res.VaultId)
	require.NoError(t, err)
	require.True(t, open)
	require.Equal(t, alice, owner)
}

func attr(evs sdk.Events, typ, key string) string {
	for _, e := range evs {
		if e.Type != typ {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == key {
				return a.Value
			}
		}
	}
	return ""
}

// While the custody deficit is non-zero, collateral withdrawals and closing a vault
// with collateral are rejected (spec M3-T6).
func TestWithdrawBlockedByDeficit(t *testing.T) {
	f := setup(t)
	id := f.open(t, alice, 50_000_000)
	require.NoError(t, f.cust.Deficit.Set(f.ctx, n(1)))
	_, err := f.k.WithdrawCollateral(f.ctx, &types.MsgWithdrawCollateral{Owner: alice.String(), VaultId: id, AmountSat: n(1_000_000), RecipientScript: wpkh})
	require.ErrorIs(t, err, custodytypes.ErrDeficit)
	_, err = f.k.CloseVault(f.ctx, &types.MsgCloseVault{Owner: alice.String(), VaultId: id, RecipientScript: wpkh})
	require.ErrorIs(t, err, custodytypes.ErrDeficit)

	require.NoError(t, f.cust.Deficit.Set(f.ctx, n(0)))
	_, err = f.k.WithdrawCollateral(f.ctx, &types.MsgWithdrawCollateral{Owner: alice.String(), VaultId: id, AmountSat: n(1_000_000), RecipientScript: wpkh})
	require.NoError(t, err)
}
