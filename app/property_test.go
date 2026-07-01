package app_test

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/testutil/apptest"
	liqkeeper "github.com/OWNER/keystone/x/liquidation/keeper"
	liqtypes "github.com/OWNER/keystone/x/liquidation/types"
	stablecointypes "github.com/OWNER/keystone/x/stablecoin/types"
	vaultkeeper "github.com/OWNER/keystone/x/vault/keeper"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

// Acceptance check 2: 5,000 random sequences (blocks) of open / credit / mint / repay /
// withdraw / close / pool / auction operations and price changes, with every
// implemented invariant checked after each block. Run as 50 independent seeded chains
// of 100 blocks. PROPERTY_CHAINS / PROPERTY_BLOCKS override the sizes.
func TestPropertyInvariants(t *testing.T) {
	chains, blocks := envInt("PROPERTY_CHAINS", 50), envInt("PROPERTY_BLOCKS", 100)
	start := time.Now()
	totals = map[string]int{}
	for seed := 1; seed <= chains; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { runProperty(t, int64(seed), blocks) })
	}
	t.Logf("%d sequences (blocks) in %s; liquidations %v", chains*blocks, time.Since(start), totals)
	if chains*blocks >= 5_000 {
		require.Positive(t, totals["liq_POOL"], "the run must exercise pool liquidations")
		require.Positive(t, totals["liq_AUCTION"], "the run must exercise auction liquidations")
	}
}

// totals counts liquidations by mode across every chain of the run.
var totals map[string]int

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

type actor struct {
	key  *secp256k1.PrivKey
	addr sdk.AccAddress
}

func runProperty(t *testing.T, seed int64, blocks int) {
	c := apptest.New(t, 1)
	rng := rand.New(rand.NewSource(seed))
	actors := []actor{
		{c.Users[0].Key, c.Users[0].Addr},
		{c.Users[1].Key, c.Users[1].Addr},
		{c.Operators[0].AccKey, c.Operators[0].Addr},
	}
	price := uint64(100_000_000_000)
	c.Price.SetMicro(price)
	c.NextBlock(nil)
	c.NextBlock(nil)
	vq := vaultkeeper.NewQuerier(c.App.VaultKeeper)
	lq := liqkeeper.NewQuerier(c.App.LiquidationKeeper)
	ops := map[string]int{}

	for b := 0; b < blocks; b++ {
		ctx := c.Context()
		// Price: random walk, rare outages, occasional long gaps (fee accrual).
		switch r := rng.Float64(); {
		case r < 0.03:
			c.Price.Down()
		case r < 0.35:
			price = price * uint64(70+rng.Intn(46)) / 100 // ×0.70 .. ×1.15
			if price < 10_000_000_000 {
				price = 10_000_000_000
			}
			c.Price.SetMicro(price)
		case r < 0.45:
			c.Price.SetMicro(price)
		}
		if rng.Float64() < 0.05 {
			c.Time = c.Time.Add(time.Duration(rng.Intn(30*24)) * time.Hour)
		}

		var txs [][]byte
		var sent []chainhash.Hash
		perActor := map[string]int{}
		send := func(a actor, name string, msg sdk.Msg) {
			if perActor[a.addr.String()] >= 4 {
				return
			}
			perActor[a.addr.String()]++
			ops[name]++
			txs = append(txs, c.SignTx(a.key, msg))
		}
		for i, nops := 0, rng.Intn(7); i < nops; i++ {
			a := actors[rng.Intn(len(actors))]
			own, err := vq.VaultsByOwner(ctx, &vaulttypes.QueryVaultsByOwnerRequest{Owner: a.addr.String()})
			require.NoError(t, err)
			var vault *vaulttypes.VaultWithDebt
			if len(own.Vaults) > 0 {
				vault = &own.Vaults[rng.Intn(len(own.Vaults))]
			}
			switch k := rng.Intn(11); {
			case k == 0 || vault == nil:
				send(a, "open", &vaulttypes.MsgOpenVault{Owner: a.addr.String()})
			case k == 1:
				// A Bitcoin deposit (fake regtest chain); 1 in 10 is below MinDepositSat
				// and goes to the fee reserve. Credited two blocks later.
				sat := int64(100_000 + rng.Intn(200_000_000))
				if rng.Intn(10) == 0 {
					sat = int64(330 + rng.Intn(9_000))
				}
				sent = append(sent, c.Bitcoin.Send(c.DepositScript(vault.Vault.Id), sat))
				ops["deposit"]++
			case k <= 3:
				// 10–90% of the vault's remaining capacity at MCR 150% and the current price,
				// sometimes above it (rejected), so most mints land.
				capacity := vault.Vault.CollateralSat.Mul(sdkmath.NewIntFromUint64(price)).QuoRaw(150_000_000).Sub(vault.Debt)
				amt := capacity.MulRaw(int64(10 + rng.Intn(90))).QuoRaw(100)
				if !amt.GT(sdkmath.NewInt(100_000_000)) {
					amt = sdkmath.NewInt(int64(100+rng.Intn(5_000)) * 1_000_000)
				}
				send(a, "mint", &vaulttypes.MsgMint{Owner: a.addr.String(), VaultId: vault.Vault.Id, Amount: amt})
			case k == 4:
				amt := vault.Debt.MulRaw(int64(rng.Intn(120))).QuoRaw(100).AddRaw(1)
				send(a, "repay", &vaulttypes.MsgRepay{Sender: a.addr.String(), VaultId: vault.Vault.Id, Amount: amt})
			case k == 5:
				sat := sdkmath.NewInt(int64(10_000 + rng.Intn(50_000_000)))
				send(a, "withdraw", &vaulttypes.MsgWithdrawCollateral{Owner: a.addr.String(), VaultId: vault.Vault.Id, AmountSat: sat})
			case k == 6:
				send(a, "close", &vaulttypes.MsgCloseVault{Owner: a.addr.String(), VaultId: vault.Vault.Id})
			case k == 7 || k == 8 && rng.Intn(2) == 0:
				bal := c.App.BankKeeper.GetBalance(ctx, a.addr, stablecointypes.Denom).Amount
				if bal.IsPositive() {
					amt := bal.MulRaw(int64(1 + rng.Intn(100))).QuoRaw(100)
					if amt.IsPositive() {
						send(a, "pool_deposit", &liqtypes.MsgDepositPool{Depositor: a.addr.String(), Amount: amt})
					}
				}
			case k == 8:
				amt := sdkmath.NewInt(int64(rng.Intn(30_000)) * 1_000_000)
				send(a, "pool_withdraw", &liqtypes.MsgWithdrawPool{Depositor: a.addr.String(), Amount: amt})
			case k == 9:
				cl, err := c.App.CustodyKeeper.GetClaimable(ctx, a.addr)
				require.NoError(t, err)
				if cl.IsPositive() {
					send(a, "deposit_claim", &vaulttypes.MsgDepositClaim{Owner: a.addr.String(), VaultId: vault.Vault.Id, AmountSat: cl})
				}
			default:
				auctions, err := lq.Auctions(ctx, &liqtypes.QueryAuctionsRequest{})
				require.NoError(t, err)
				if len(auctions.Auctions) > 0 {
					au := auctions.Auctions[rng.Intn(len(auctions.Auctions))]
					send(a, "take", &liqtypes.MsgTakeCollateral{
						Taker: a.addr.String(), AuctionId: au.Auction.Id,
						MaxSat: sdkmath.NewInt(int64(1 + rng.Intn(100_000_000))), MaxPrice: sdkmath.NewInt(1_000_000_000_000),
					})
				}
			}
		}
		if len(sent) > 0 {
			c.Bitcoin.Mine(1)
		}
		// Rare Bitcoin reorgs: the last 1–3 blocks are replaced; a deposit sent this
		// block may be dropped. Credited deposits in replaced blocks are reverted (a
		// shortfall becomes custody deficit) and re-credited if re-mined.
		if rng.Intn(40) == 0 {
			var drop []chainhash.Hash
			if len(sent) > 0 && rng.Intn(2) == 0 {
				drop = sent[:1]
			}
			c.Bitcoin.Reorg(1+rng.Intn(3), drop...)
			ops["btc_reorg"]++
		}
		blk := c.NextBlock(txs)
		for _, r := range blk.Finalize.TxResults[1:] {
			if r.Code != 0 {
				ops["rejected"]++
			}
		}
		for _, e := range blk.Finalize.Events {
			if e.Type == liqtypes.EventTypeVaultLiquidated {
				for _, a := range e.Attributes {
					if a.Key == liqtypes.AttributeKeyMode {
						ops["liq_"+a.Value]++
						totals["liq_"+a.Value]++
					}
				}
			}
		}
		require.NoError(t, c.App.CheckInvariants(c.Context()), "seed %d block %d", seed, b)
	}
	cl, _ := custodyTotal(c)
	t.Logf("seed %d: ops %v, claimable %s sat", seed, ops, cl)
}

func custodyTotal(c *apptest.Chain) (sdkmath.Int, error) {
	q := c.App.CustodyKeeper
	return q.TotalClaimable(c.Context())
}
