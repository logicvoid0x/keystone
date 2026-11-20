package keeper

import (
	"context"
	"strconv"

	"cosmossdk.io/collections"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	oracletypes "github.com/logicvoid0x/keystone/x/oracle/types"
	"github.com/logicvoid0x/keystone/x/vault/types"
)

func uintStr(v uint64) string { return strconv.FormatUint(v, 10) }

// ownedVault loads a vault and checks the signer owns it and it is not closed.
func (k *Keeper) ownedVault(ctx context.Context, id uint64, owner string) (types.Vault, error) {
	v, err := k.GetVault(ctx, id)
	if err != nil {
		return v, err
	}
	if v.Owner != owner {
		return v, errorsmod.Wrapf(types.ErrNotOwner, "vault %d", id)
	}
	if v.Status == types.VAULT_STATUS_CLOSED {
		return v, errorsmod.Wrapf(types.ErrVaultStatus, "vault %d is closed", id)
	}
	return v, nil
}

func emit(ctx context.Context, typ string, attrs ...sdk.Attribute) {
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(typ, attrs...))
}

// OpenVault opens a vault for owner and returns its id and deposit address (empty
// while no custody epoch is ACTIVE; design §9.7 step 1).
func (k *Keeper) OpenVault(ctx context.Context, msg *types.MsgOpenVault) (uint64, string, error) {
	if err := msg.ValidateBasic(); err != nil {
		return 0, "", err
	}
	owner := sdk.MustAccAddressFromBech32(msg.Owner)
	id, err := k.NextVaultID.Next(ctx)
	if err != nil {
		return 0, "", err
	}
	id++ // vault ids start at 1
	v := types.Vault{Id: id, Owner: owner.String(), Art: sdkmath.ZeroInt(), CollateralSat: sdkmath.ZeroInt(), Status: types.VAULT_STATUS_OPEN}
	if err := k.setVault(ctx, nil, v); err != nil {
		return 0, "", err
	}
	if err := k.OwnerIndex.Set(ctx, collections.Join(owner, id)); err != nil {
		return 0, "", err
	}
	addr, err := k.custody.RegisterVault(ctx, id)
	if err != nil {
		return 0, "", err
	}
	emit(ctx, types.EventTypeVaultOpened,
		sdk.NewAttribute(types.AttributeKeyID, uintStr(id)),
		sdk.NewAttribute(types.AttributeKeyOwner, owner.String()),
		sdk.NewAttribute(types.AttributeKeyAddress, addr))
	return id, addr, nil
}

// Mint mints amount against a vault (design §7.2): owner; price ACTIVE; mints not
// paused; debt after ≥ Dust; total debt ≤ ceiling; CR after ≥ MCR at min(spot, TWAP).
func (k *Keeper) Mint(ctx context.Context, msg *types.MsgMint) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	v, err := k.ownedVault(ctx, msg.VaultId, msg.Owner)
	if err != nil {
		return err
	}
	if v.Status != types.VAULT_STATUS_ACTIVE {
		return errorsmod.Wrapf(types.ErrVaultStatus, "vault %d is %s", v.Id, v.Status)
	}
	ps, err := k.oracle.GetPriceState(ctx)
	if err != nil {
		return err
	}
	if ps.Status != oracletypes.PRICE_STATUS_ACTIVE {
		return errorsmod.Wrapf(types.ErrPriceStatus, "price is %s", ps.Status)
	}
	paused, err := k.stablecoin.AreMintsPaused(ctx)
	if err != nil {
		return err
	}
	if paused {
		return types.ErrMintsPaused
	}
	price, ok, err := k.oracle.CollateralPrice(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errorsmod.Wrap(types.ErrPriceStatus, "no collateral price")
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	rate, err := k.Rate(ctx)
	if err != nil {
		return err
	}
	dArt := types.ArtForMint(msg.Amount, rate)
	next := v
	next.Art = v.Art.Add(dArt)
	debtAfter := types.DebtOf(next.Art, rate)
	if debtAfter.LT(p.Dust) {
		return errorsmod.Wrapf(types.ErrDust, "debt %s < dust %s", debtAfter, p.Dust)
	}
	totalArt, err := k.TotalArt.Get(ctx)
	if err != nil {
		return err
	}
	if types.DebtOf(totalArt.Add(dArt), rate).GT(p.GlobalDebtCeiling) {
		return errorsmod.Wrapf(types.ErrCeiling, "ceiling %s", p.GlobalDebtCeiling)
	}
	if !types.MeetsMCR(next.CollateralSat, debtAfter, price, p.McrBps) {
		return errorsmod.Wrapf(types.ErrBelowMCR, "vault %d at price %s", v.Id, price)
	}

	if err := k.setVault(ctx, &v, next); err != nil {
		return err
	}
	if err := k.addTotalArt(ctx, dArt); err != nil {
		return err
	}
	if err := k.CeilingMark.Set(ctx, types.CeilingMark{Rate: rate, Ceiling: p.GlobalDebtCeiling}); err != nil {
		return err
	}
	if err := k.addSlack(ctx, opSlack(rate)); err != nil {
		return err
	}
	if err := k.stablecoin.Mint(ctx, sdk.MustAccAddressFromBech32(msg.Owner), msg.Amount); err != nil {
		return err
	}
	emit(ctx, types.EventTypeMint,
		sdk.NewAttribute(types.AttributeKeyID, uintStr(v.Id)),
		sdk.NewAttribute(types.AttributeKeyAmount, msg.Amount.String()))
	return nil
}

// Repay burns up to amount from the sender against a vault's debt (anyone may repay).
// Paying at least the full debt burns exactly the debt and sets art = 0; otherwise
// art −= floor(r × RAY / rate) and the remaining debt must be 0 or ≥ Dust.
func (k *Keeper) Repay(ctx context.Context, msg *types.MsgRepay) (sdkmath.Int, error) {
	if err := msg.ValidateBasic(); err != nil {
		return sdkmath.Int{}, err
	}
	v, err := k.GetVault(ctx, msg.VaultId)
	if err != nil {
		return sdkmath.Int{}, err
	}
	rate, err := k.Rate(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	debt := types.DebtOf(v.Art, rate)
	if !debt.IsPositive() {
		return sdkmath.Int{}, errorsmod.Wrapf(types.ErrInvalidAmount, "vault %d has no debt", v.Id)
	}
	next := v
	var repaid, dArt, slack sdkmath.Int
	if msg.Amount.GTE(debt) {
		repaid, dArt, slack = debt, v.Art, sdkmath.OneInt()
		next.Art = sdkmath.ZeroInt()
	} else {
		p, err := k.Params.Get(ctx)
		if err != nil {
			return sdkmath.Int{}, err
		}
		repaid, dArt, slack = msg.Amount, types.ArtForRepay(msg.Amount, rate), opSlack(rate)
		next.Art = v.Art.Sub(dArt)
		if rem := types.DebtOf(next.Art, rate); rem.IsPositive() && rem.LT(p.Dust) {
			return sdkmath.Int{}, errorsmod.Wrapf(types.ErrDust, "remaining debt %s < dust %s", rem, p.Dust)
		}
	}
	if err := k.stablecoin.BurnFrom(ctx, sdk.MustAccAddressFromBech32(msg.Sender), repaid); err != nil {
		return sdkmath.Int{}, err
	}
	if err := k.setVault(ctx, &v, next); err != nil {
		return sdkmath.Int{}, err
	}
	if err := k.addTotalArt(ctx, dArt.Neg()); err != nil {
		return sdkmath.Int{}, err
	}
	if err := k.addSlack(ctx, slack); err != nil {
		return sdkmath.Int{}, err
	}
	emit(ctx, types.EventTypeRepay,
		sdk.NewAttribute(types.AttributeKeyID, uintStr(v.Id)),
		sdk.NewAttribute(types.AttributeKeyAmount, repaid.String()))
	return repaid, nil
}

// WithdrawCollateral debits collateral (design §7.2, §9.8 step 1): owner; price not
// STALE; CR after ≥ MCR at min(spot, TWAP) or debt = 0; amount ≥ MinWithdrawSat; a
// P2TR, P2WPKH or P2WSH recipient; no custody deficit (spec M3-T6). The satoshis leave
// the vault at once and a withdrawal request is queued; it returns the request id.
func (k *Keeper) WithdrawCollateral(ctx context.Context, msg *types.MsgWithdrawCollateral) (uint64, error) {
	if err := msg.ValidateBasic(); err != nil {
		return 0, err
	}
	if err := k.custody.ValidateWithdrawal(ctx, msg.RecipientScript, msg.AmountSat); err != nil {
		return 0, err
	}
	v, err := k.ownedVault(ctx, msg.VaultId, msg.Owner)
	if err != nil {
		return 0, err
	}
	if msg.AmountSat.GT(v.CollateralSat) {
		return 0, errorsmod.Wrapf(types.ErrInsufficient, "vault %d has %s sat", v.Id, v.CollateralSat)
	}
	price, ok, err := k.oracle.CollateralPrice(ctx)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errorsmod.Wrap(types.ErrPriceStatus, "price is stale")
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	debt, err := k.Debt(ctx, v)
	if err != nil {
		return 0, err
	}
	next := v
	next.CollateralSat = v.CollateralSat.Sub(msg.AmountSat)
	if !types.MeetsMCR(next.CollateralSat, debt, price, p.McrBps) {
		return 0, errorsmod.Wrapf(types.ErrBelowMCR, "vault %d", v.Id)
	}
	return k.withdrawCollateral(ctx, v, next, msg.AmountSat, msg.RecipientScript)
}

// withdrawCollateral takes sat out of the vault and the credited counter and queues
// a withdrawal request to script.
func (k *Keeper) withdrawCollateral(ctx context.Context, prev, next types.Vault, sat sdkmath.Int, script []byte) (uint64, error) {
	if err := k.setVault(ctx, &prev, next); err != nil {
		return 0, err
	}
	if err := k.custody.SubCredited(ctx, sat); err != nil {
		return 0, err
	}
	id, err := k.custody.QueueWithdrawal(ctx, sdk.MustAccAddressFromBech32(prev.Owner), script, sat)
	if err != nil {
		return 0, err
	}
	emit(ctx, types.EventTypeCollateralWithdrawn,
		sdk.NewAttribute(types.AttributeKeyID, uintStr(prev.Id)),
		sdk.NewAttribute(types.AttributeKeySat, sat.String()),
		sdk.NewAttribute(types.AttributeKeyRequestID, uintStr(id)))
	return id, nil
}

// CloseVault closes a vault with no debt and withdraws all its collateral to
// recipient_script. Collateral below MinWithdrawSat cannot be paid out on its own and
// goes to the owner's claimable balance instead. It returns the
// request id (0 if none).
func (k *Keeper) CloseVault(ctx context.Context, msg *types.MsgCloseVault) (uint64, error) {
	if err := msg.ValidateBasic(); err != nil {
		return 0, err
	}
	v, err := k.ownedVault(ctx, msg.VaultId, msg.Owner)
	if err != nil {
		return 0, err
	}
	if v.Art.IsPositive() {
		return 0, errorsmod.Wrapf(types.ErrHasDebt, "vault %d", v.Id)
	}
	next := v
	next.CollateralSat = sdkmath.ZeroInt()
	next.Status = types.VAULT_STATUS_CLOSED
	if !v.CollateralSat.IsPositive() {
		return 0, k.setVault(ctx, &v, next)
	}
	if err := k.custody.CheckNoDeficit(ctx); err != nil {
		return 0, err
	}
	cp, err := k.custody.GetParams(ctx)
	if err != nil {
		return 0, err
	}
	if v.CollateralSat.LT(cp.MinWithdrawSat) {
		if err := k.setVault(ctx, &v, next); err != nil {
			return 0, err
		}
		if err := k.custody.SubCredited(ctx, v.CollateralSat); err != nil {
			return 0, err
		}
		return 0, k.custody.CreditClaimable(ctx, sdk.MustAccAddressFromBech32(v.Owner), v.CollateralSat)
	}
	if err := k.custody.ValidateWithdrawal(ctx, msg.RecipientScript, v.CollateralSat); err != nil {
		return 0, err
	}
	return k.withdrawCollateral(ctx, v, next, v.CollateralSat, msg.RecipientScript)
}

// DepositClaim moves claimable BTC into a vault. It counts as a credit: allowed only
// within the treasury-cap headroom.
func (k *Keeper) DepositClaim(ctx context.Context, msg *types.MsgDepositClaim) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	v, err := k.ownedVault(ctx, msg.VaultId, msg.Owner)
	if err != nil {
		return err
	}
	headroom, err := k.custody.CapHeadroom(ctx)
	if err != nil {
		return err
	}
	if msg.AmountSat.GT(headroom) {
		return errorsmod.Wrapf(types.ErrInvalidAmount, "treasury cap headroom is %s sat", headroom)
	}
	if err := k.custody.DebitClaimable(ctx, sdk.MustAccAddressFromBech32(msg.Owner), msg.AmountSat); err != nil {
		return err
	}
	_, err = k.CreditCollateral(ctx, v.Id, msg.AmountSat)
	return err
}
