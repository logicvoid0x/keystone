package app

import (
	"encoding/binary"

	errorsmod "cosmossdk.io/errors"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
)

// TxCountStoreKey names the transient store that counts txs per signer in a block.
const TxCountStoreKey = "ante_txcount"

// ErrTooManyTxs is returned when a signer exceeds MaxTxPerAccountPerBlock.
var ErrTooManyTxs = errorsmod.Register("keystone_ante", 2, "too many transactions from one signer in this block")

// NewAnteHandler is the standard SDK v0.55 ante chain (ante.NewAnteHandler) with the
// staking message rules (design §4.2, M7) after ValidateBasic and the
// MaxTxPerAccountPerBlock decorator after signature verification. Fees are zero
// (minimum-gas-prices = 0ukusd); tx0 never reaches the ante handler.
func NewAnteHandler(opts ante.HandlerOptions, txCountKey storetypes.StoreKey, limit uint64, allow Allowlist) (sdk.AnteHandler, error) {
	if opts.AccountKeeper == nil || opts.BankKeeper == nil || opts.SignModeHandler == nil || allow == nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrLogic, "account keeper, bank keeper, sign mode handler and allowlist are required")
	}
	return sdk.ChainAnteDecorators(
		ante.NewSetUpContextDecorator(),
		ante.NewExtensionOptionsDecorator(opts.ExtensionOptionChecker),
		ante.NewValidateBasicDecorator(),
		NewStakingMsgDecorator(allow),
		ante.NewTxTimeoutHeightDecorator(),
		ante.NewValidateMemoDecorator(opts.AccountKeeper),
		ante.NewConsumeGasForTxSizeDecorator(opts.AccountKeeper),
		ante.NewDeductFeeDecorator(opts.AccountKeeper, opts.BankKeeper, opts.FeegrantKeeper, opts.TxFeeChecker),
		ante.NewSetPubKeyDecorator(opts.AccountKeeper),
		ante.NewValidateSigCountDecorator(opts.AccountKeeper),
		ante.NewSigGasConsumeDecorator(opts.AccountKeeper, opts.SigGasConsumer),
		ante.NewSigVerificationDecorator(opts.AccountKeeper, opts.SignModeHandler, opts.SigVerifyOptions...),
		NewMaxTxPerAccountDecorator(txCountKey, limit),
		ante.NewIncrementSequenceDecorator(opts.AccountKeeper),
	), nil
}

// MaxTxPerAccountDecorator rejects a tx when any of its signers already has `limit`
// txs in the current block. Counts live in a transient store, which is cleared at
// every commit.
type MaxTxPerAccountDecorator struct {
	key   storetypes.StoreKey
	limit uint64
}

func NewMaxTxPerAccountDecorator(key storetypes.StoreKey, limit uint64) MaxTxPerAccountDecorator {
	return MaxTxPerAccountDecorator{key: key, limit: limit}
}

func (d MaxTxPerAccountDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return ctx, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx has no signers")
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return ctx, err
	}
	store := ctx.TransientStore(d.key)
	counts := make([]uint64, len(signers))
	for i, s := range signers {
		if bz := store.Get(s); len(bz) == 8 {
			counts[i] = binary.BigEndian.Uint64(bz)
		}
		if counts[i] >= d.limit {
			return ctx, errorsmod.Wrapf(ErrTooManyTxs, "signer %s: limit %d", sdk.AccAddress(s), d.limit)
		}
	}
	for i, s := range signers {
		store.Set(s, binary.BigEndian.AppendUint64(nil, counts[i]+1))
	}
	return next(ctx, tx, simulate)
}
