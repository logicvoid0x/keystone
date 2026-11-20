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
	vaulttypes "github.com/logicvoid0x/keystone/x/vault/types"
)

// AuctionPrice is the current price of auction a at height: start × (1 −
// AuctionStepBps/10,000)^(blocks since start), in RAY fixed point, floored.
func AuctionPrice(a types.Auction, height int64, p types.Params) sdkmath.Int {
	k := height - a.StartHeight
	if k <= 0 {
		return a.StartPrice
	}
	factor := vaulttypes.RAY.MulRaw(int64(10_000 - p.AuctionStepBps)).QuoRaw(10_000)
	return a.StartPrice.Mul(vaulttypes.Rpow(factor, uint64(k), vaulttypes.RAY)).Quo(vaulttypes.RAY)
}

// GetAuction returns an auction.
func (k *Keeper) GetAuction(ctx context.Context, id uint64) (types.Auction, error) {
	a, err := k.Auctions.Get(ctx, id)
	if errors.Is(err, collections.ErrNotFound) {
		return a, errorsmod.Wrapf(types.ErrAuctionNotFound, "%d", id)
	}
	return a, err
}

// tickAuctions resets auctions whose price fell below AuctionResetBps of the start or
// that ran AuctionMaxBlocks, restarting at the current spot × AuctionStartBps. With a
// STALE price nothing resets.
func (k *Keeper) tickAuctions(ctx context.Context) error {
	spot, ok, err := k.oracle.LiquidationPrice(ctx)
	if err != nil || !ok {
		return err
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	var reset []types.Auction
	err = k.Auctions.Walk(ctx, nil, func(_ uint64, a types.Auction) (bool, error) {
		cur := AuctionPrice(a, height, p)
		floor := a.StartPrice.MulRaw(int64(p.AuctionResetBps)).QuoRaw(10_000)
		if cur.LT(floor) || height-a.StartHeight >= int64(p.AuctionMaxBlocks) {
			a.StartPrice = spot.MulRaw(int64(p.AuctionStartBps)).QuoRaw(10_000)
			a.StartHeight = height
			reset = append(reset, a)
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, a := range reset {
		if err := k.Auctions.Set(ctx, a.Id, a); err != nil {
			return err
		}
		emit(ctx, types.EventTypeAuctionReset, sdk.NewAttribute(types.AttributeKeyID, fmt.Sprint(a.Id)))
	}
	return nil
}

// TakeCollateral buys up to max_sat at the auction's current price (design §8.4 step
// 3). The taker pays ceil(sat × price / 1e8) KUSD, never more than tab − raised; the
// payment burns the auction's sin first and the rest (penalty) goes to surplus. The
// satoshis are credited to the taker's claimable balance. When proceeds reach the tab
// or the collateral is gone, leftover collateral returns to the vault.
func (k *Keeper) TakeCollateral(ctx context.Context, msg *types.MsgTakeCollateral) (sat, paid sdkmath.Int, err error) {
	if err := msg.ValidateBasic(); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	a, err := k.GetAuction(ctx, msg.AuctionId)
	if err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	price := AuctionPrice(a, sdk.UnwrapSDKContext(ctx).BlockHeight(), p)
	if !price.IsPositive() {
		return sdkmath.Int{}, sdkmath.Int{}, errorsmod.Wrap(types.ErrInvalidAmount, "auction price is zero")
	}
	if price.GT(msg.MaxPrice) {
		return sdkmath.Int{}, sdkmath.Int{}, errorsmod.Wrapf(types.ErrPriceTooHigh, "price %s > max %s", price, msg.MaxPrice)
	}
	owe := a.Tab.Sub(a.Raised)
	sat = sdkmath.MinInt(msg.MaxSat, a.CollateralSat)
	paid = ceilDiv(sat.Mul(price), vaulttypes.SatPerBTC)
	if paid.GT(owe) {
		paid = owe
		sat = sdkmath.MinInt(a.CollateralSat, ceilDiv(owe.Mul(vaulttypes.SatPerBTC), price))
	}
	if !paid.IsPositive() || !sat.IsPositive() {
		return sdkmath.Int{}, sdkmath.Int{}, errorsmod.Wrap(types.ErrInvalidAmount, "nothing to take")
	}
	taker := sdk.MustAccAddressFromBech32(msg.Taker)
	if err := k.bank.SendCoinsFromAccountToModule(ctx, taker, types.ModuleName, kusd(paid)); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	toSin := sdkmath.MinInt(paid, a.SinOwed)
	if toSin.IsPositive() {
		if err := k.stablecoin.BurnAgainstSin(ctx, types.ModuleName, toSin); err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
	}
	if rest := paid.Sub(toSin); rest.IsPositive() {
		if err := k.stablecoin.AddToSurplus(ctx, types.ModuleName, rest); err != nil {
			return sdkmath.Int{}, sdkmath.Int{}, err
		}
	}
	if err := k.custody.CreditClaimable(ctx, taker, sat); err != nil {
		return sdkmath.Int{}, sdkmath.Int{}, err
	}
	a.SinOwed = a.SinOwed.Sub(toSin)
	a.Raised = a.Raised.Add(paid)
	a.CollateralSat = a.CollateralSat.Sub(sat)
	emit(ctx, types.EventTypeAuctionTaken,
		sdk.NewAttribute(types.AttributeKeyID, fmt.Sprint(a.Id)),
		sdk.NewAttribute(types.AttributeKeySat, sat.String()),
		sdk.NewAttribute(types.AttributeKeyPrice, price.String()))

	if a.Raised.GTE(a.Tab) || !a.CollateralSat.IsPositive() {
		return sat, paid, k.finish(ctx, a)
	}
	return sat, paid, k.Auctions.Set(ctx, a.Id, a)
}

// finish removes an auction and returns leftover collateral to its vault (within cap
// headroom; any excess goes to the owner's claimable). Unmet sin stays for surplus.
func (k *Keeper) finish(ctx context.Context, a types.Auction) error {
	if err := k.Auctions.Remove(ctx, a.Id); err != nil {
		return err
	}
	if a.CollateralSat.IsPositive() {
		if _, err := k.vault.CreditCollateral(ctx, a.VaultId, a.CollateralSat); err != nil {
			return err
		}
	}
	emit(ctx, types.EventTypeAuctionFinished, sdk.NewAttribute(types.AttributeKeyID, fmt.Sprint(a.Id)))
	return nil
}

// TotalAuctionSat sums the collateral held by running auctions.
func (k *Keeper) TotalAuctionSat(ctx context.Context) (sdkmath.Int, error) {
	total := sdkmath.ZeroInt()
	err := k.Auctions.Walk(ctx, nil, func(_ uint64, a types.Auction) (bool, error) {
		total = total.Add(a.CollateralSat)
		return false, nil
	})
	return total, err
}
