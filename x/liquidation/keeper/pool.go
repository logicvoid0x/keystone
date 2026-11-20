package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/x/liquidation/types"
)

// AUDIT: this file ports Liquity v1's StabilityPool product-sum accounting
// (StabilityPool.sol: _computeRewardsPerUnitStaked, _updateRewardSumAndProduct,
// getCompoundedLUSDDeposit, getDepositorETHGain), including its scale-change logic
// and error-feedback terms. Liquity had precision bugs in exactly this code (design
// §8.4); every rounding step below mirrors the Solidity and must be reviewed against
// it. Units: KUSD in ukusd for LUSD, satoshis for ETH, P and S in 1e18 precision.

func (k *Keeper) sumAt(ctx context.Context, epoch, scale uint64) (sdkmath.Int, error) {
	s, err := k.EpochScaleSum.Get(ctx, collections.Join(epoch, scale))
	if errors.Is(err, collections.ErrNotFound) {
		return sdkmath.ZeroInt(), nil
	}
	return s, err
}

// compounded is getCompoundedLUSDDeposit: initial × P / P_snapshot, divided by
// ScaleFactor once per scale change (0 after two), 0 after an epoch change, and 0 when
// below initial / 1e9 (Liquity's precision guard).
func (k *Keeper) compounded(ctx context.Context, d types.PoolDeposit) (sdkmath.Int, error) {
	sc, err := k.Scalars.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	if d.Epoch < sc.Epoch || !d.Initial.IsPositive() {
		return sdkmath.ZeroInt(), nil
	}
	var c sdkmath.Int
	switch sc.Scale - d.Scale {
	case 0:
		c = d.Initial.Mul(sc.P).Quo(d.PSnapshot)
	case 1:
		c = d.Initial.Mul(sc.P).Quo(d.PSnapshot).Quo(types.ScaleFactor)
	default:
		return sdkmath.ZeroInt(), nil
	}
	if c.LT(d.Initial.Quo(types.ScaleFactor)) {
		return sdkmath.ZeroInt(), nil
	}
	return c, nil
}

// gain is getDepositorETHGain: initial × (S[e][s] − S_snapshot + S[e][s+1] / ScaleFactor)
// / P_snapshot / 1e18, in satoshis.
func (k *Keeper) gain(ctx context.Context, d types.PoolDeposit) (sdkmath.Int, error) {
	if !d.Initial.IsPositive() {
		return sdkmath.ZeroInt(), nil
	}
	s0, err := k.sumAt(ctx, d.Epoch, d.Scale)
	if err != nil {
		return sdkmath.Int{}, err
	}
	s1, err := k.sumAt(ctx, d.Epoch, d.Scale+1)
	if err != nil {
		return sdkmath.Int{}, err
	}
	portion := s0.Sub(d.SSnapshot).Add(s1.Quo(types.ScaleFactor))
	return d.Initial.Mul(portion).Quo(d.PSnapshot).Quo(types.DecimalPrecision), nil
}

// Position returns a depositor's compounded deposit and pending BTC gain.
func (k *Keeper) Position(ctx context.Context, addr sdk.AccAddress) (compounded, gainSat sdkmath.Int, err error) {
	d, err := k.Deposits.Get(ctx, addr)
	if errors.Is(err, collections.ErrNotFound) {
		return sdkmath.ZeroInt(), sdkmath.ZeroInt(), nil
	}
	if err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	if compounded, err = k.compounded(ctx, d); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	gainSat, err = k.gain(ctx, d)
	return compounded, gainSat, err
}

// touch pays a depositor's pending BTC gain into custody claimable and returns the
// compounded deposit and the gain paid.
func (k *Keeper) touch(ctx context.Context, addr sdk.AccAddress) (sdkmath.Int, sdkmath.Int, error) {
	c, g, err := k.Position(ctx, addr)
	if err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	if has, err := k.Deposits.Has(ctx, addr); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	} else if has {
		if err := k.addSlack(ctx); err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
	}
	if g.IsPositive() {
		pending, err := k.PendingSat.Get(ctx)
		if err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
		if g.GT(pending) {
			// Rounding can only make the sum of gains smaller than what was seized.
			return sdkmath.Int{}, sdkmath.Int{}, fmt.Errorf("pool gain %s exceeds pending %s", g, pending)
		}
		if err := k.PendingSat.Set(ctx, pending.Sub(g)); err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
		if err := k.custody.CreditClaimable(ctx, addr, g); err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
	}
	return c, g, nil
}

// setDeposit stores a deposit of amount with snapshots of the current P, S, epoch and
// scale, or removes the record when amount is zero.
func (k *Keeper) setDeposit(ctx context.Context, addr sdk.AccAddress, amount sdkmath.Int) error {
	if !amount.IsPositive() {
		return k.Deposits.Remove(ctx, addr)
	}
	sc, err := k.Scalars.Get(ctx)
	if err != nil {
		return err
	}
	s, err := k.sumAt(ctx, sc.Epoch, sc.Scale)
	if err != nil {
		return err
	}
	return k.Deposits.Set(ctx, addr, types.PoolDeposit{
		Initial: amount, PSnapshot: sc.P, SSnapshot: s, Epoch: sc.Epoch, Scale: sc.Scale,
	})
}

func (k *Keeper) addTotal(ctx context.Context, delta sdkmath.Int) error {
	t, err := k.TotalDeposits.Get(ctx)
	if err != nil {
		return err
	}
	t = t.Add(delta)
	if t.IsNegative() {
		return fmt.Errorf("pool total deposits would be negative (%s)", t)
	}
	return k.TotalDeposits.Set(ctx, t)
}

// DepositPool deposits KUSD into the pool, paying pending gains first.
func (k *Keeper) DepositPool(ctx context.Context, msg *types.MsgDepositPool) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	addr := sdk.MustAccAddressFromBech32(msg.Depositor)
	c, _, err := k.touch(ctx, addr)
	if err != nil {
		return err
	}
	if err := k.bank.SendCoinsFromAccountToModule(ctx, addr, types.ModuleName, kusd(msg.Amount)); err != nil {
		return err
	}
	if err := k.addTotal(ctx, msg.Amount); err != nil {
		return err
	}
	return k.setDeposit(ctx, addr, c.Add(msg.Amount))
}

// WithdrawPool withdraws up to amount of the compounded deposit and pays pending gains.
// It is blocked while the price is STALE or any vault is liquidatable at the spot
// (design §11.1, §6.4).
func (k *Keeper) WithdrawPool(ctx context.Context, msg *types.MsgWithdrawPool) (withdrawn, gainSat sdkmath.Int, err error) {
	if err := msg.ValidateBasic(); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	addr := sdk.MustAccAddressFromBech32(msg.Depositor)
	if has, err := k.Deposits.Has(ctx, addr); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	} else if !has {
		return sdkmath.Int{}, sdkmath.Int{}, types.ErrNoDeposit
	}
	if msg.Amount.IsPositive() {
		price, ok, err := k.oracle.LiquidationPrice(ctx)
		if err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
		if !ok {
			return sdkmath.Int{}, sdkmath.Int{}, errorsmod.Wrap(types.ErrWithdrawBlocked, "price is stale")
		}
		liq, err := k.vault.AnyLiquidatable(ctx, price)
		if err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
		if liq {
			return sdkmath.Int{}, sdkmath.Int{}, errorsmod.Wrap(types.ErrWithdrawBlocked, "a vault is liquidatable")
		}
	}
	c, g, err := k.touch(ctx, addr)
	if err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	w := sdkmath.MinInt(msg.Amount, c)
	if w.IsPositive() {
		if err := k.addTotal(ctx, w.Neg()); err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
		if err := k.bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, addr, kusd(w)); err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
	}
	if err := k.setDeposit(ctx, addr, c.Sub(w)); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	if err := k.sweepIfEmpty(ctx); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	return w, g, nil
}

// sweepIfEmpty moves rounding dust left in the pool to surplus once no depositor
// remains, so the pool balance never exceeds what depositors are owed by more than
// rounding (invariant 8).
func (k *Keeper) sweepIfEmpty(ctx context.Context) error {
	iter, err := k.Deposits.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	empty := !iter.Valid()
	iter.Close()
	if !empty {
		return nil
	}
	if err := k.TotalDeposits.Set(ctx, sdkmath.ZeroInt()); err != nil {
		return err
	}
	if err := k.PoolSlack.Set(ctx, sdkmath.ZeroInt()); err != nil {
		return err
	}
	if dust := k.ModuleBalance(ctx); dust.IsPositive() {
		return k.stablecoin.AddToSurplus(ctx, types.ModuleName, dust)
	}
	return nil
}

// offset absorbs debtLoss ukusd of depositor loss and collSat of collateral gain
// (Liquity _computeRewardsPerUnitStaked + _updateRewardSumAndProduct). The caller
// burns the KUSD; offset updates P, S, totals and the pending satoshis.
func (k *Keeper) offset(ctx context.Context, debtLoss, collSat sdkmath.Int) error {
	total, err := k.TotalDeposits.Get(ctx)
	if err != nil {
		return err
	}
	if !total.IsPositive() {
		return errors.New("offset with an empty pool")
	}
	if debtLoss.GT(total) {
		return fmt.Errorf("pool loss %s exceeds deposits %s", debtLoss, total)
	}
	errs, err := k.Errors.Get(ctx)
	if err != nil {
		return err
	}
	dp := types.DecimalPrecision

	// AUDIT: per-unit-staked rewards with error feedback.
	collNum := collSat.Mul(dp).Add(errs.CollError)
	var lossPerUnit sdkmath.Int
	switch {
	case debtLoss.Equal(total):
		lossPerUnit = dp
		errs.DebtError = sdkmath.ZeroInt()
	case debtLoss.IsZero():
		lossPerUnit = sdkmath.ZeroInt()
	default:
		lossNum := debtLoss.Mul(dp).Sub(errs.DebtError)
		lossPerUnit = lossNum.Quo(total).AddRaw(1) // rounded up: depositors lose slightly more
		errs.DebtError = lossPerUnit.Mul(total).Sub(lossNum)
	}
	if lossPerUnit.GT(dp) {
		return fmt.Errorf("loss per unit %s above 1e18", lossPerUnit)
	}
	gainPerUnit := collNum.Quo(total)
	errs.CollError = collNum.Sub(gainPerUnit.Mul(total))
	if err := k.Errors.Set(ctx, errs); err != nil {
		return err
	}

	// AUDIT: S and P update with scale change and epoch reset.
	sc, err := k.Scalars.Get(ctx)
	if err != nil {
		return err
	}
	curS, err := k.sumAt(ctx, sc.Epoch, sc.Scale)
	if err != nil {
		return err
	}
	if err := k.EpochScaleSum.Set(ctx, collections.Join(sc.Epoch, sc.Scale), curS.Add(gainPerUnit.Mul(sc.P))); err != nil {
		return err
	}
	factor := dp.Sub(lossPerUnit)
	emptied := false
	switch {
	case factor.IsZero():
		sc = types.PoolScalars{P: dp, Epoch: sc.Epoch + 1, Scale: 0}
		emptied = true
	case sc.P.Mul(factor).Quo(dp).LT(types.ScaleFactor):
		sc.P = sc.P.Mul(factor).Mul(types.ScaleFactor).Quo(dp)
		sc.Scale++
	default:
		sc.P = sc.P.Mul(factor).Quo(dp)
	}
	if !sc.P.IsPositive() {
		return errors.New("pool P reached zero")
	}
	if err := k.Scalars.Set(ctx, sc); err != nil {
		return err
	}

	if err := k.addTotal(ctx, debtLoss.Neg()); err != nil {
		return err
	}
	if err := k.addSlack(ctx); err != nil {
		return err
	}
	pending, err := k.PendingSat.Get(ctx)
	if err != nil {
		return err
	}
	if err := k.PendingSat.Set(ctx, pending.Add(collSat)); err != nil {
		return err
	}
	if emptied {
		// Every deposit compounds to zero in the new epoch. Any remainder of the old
		// total is rounding dust with no owner.
		rest, err := k.TotalDeposits.Get(ctx)
		if err != nil {
			return err
		}
		if err := k.PoolSlack.Set(ctx, sdkmath.ZeroInt()); err != nil {
			return err
		}
		if rest.IsPositive() {
			if err := k.TotalDeposits.Set(ctx, sdkmath.ZeroInt()); err != nil {
				return err
			}
			return k.stablecoin.AddToSurplus(ctx, types.ModuleName, rest)
		}
	}
	return nil
}

// addSlack raises the invariant-8 rounding bound: a deposit touch
// can strand up to 1 ukusd of compounding dust, and a liquidation offset up to 1.
func (k *Keeper) addSlack(ctx context.Context) error {
	s, err := k.PoolSlack.Get(ctx)
	if err != nil {
		return err
	}
	return k.PoolSlack.Set(ctx, s.AddRaw(1))
}
