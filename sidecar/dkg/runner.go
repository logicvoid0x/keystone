package dkg

import (
	"context"
	"fmt"
	"time"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
)

// ChainView loads the participant's View from committed state.
type ChainView interface {
	Load(operator sdk.AccAddress) (View, error)
}

// Submitter sends messages in one transaction signed by the hot key.
type Submitter interface {
	Submit(ctx context.Context, msgs []sdk.Msg) error
}

// Runner polls the committed state and submits the participant's messages.
type Runner struct {
	P      *Participant
	View   ChainView
	Submit Submitter
	Logger log.Logger
	Poll   time.Duration

	last int64
}

// Run steps the participant once per new committed height until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	poll := r.Poll
	if poll <= 0 {
		poll = time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v, err := r.View.Load(r.P.Operator)
		if err != nil || v.Height == r.last {
			continue
		}
		r.last = v.Height
		msgs, err := r.P.Step(v)
		if err != nil {
			r.Logger.Error("dkg step failed", "height", v.Height, "err", err)
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		if err := r.Submit.Submit(ctx, msgs); err != nil {
			r.Logger.Error("dkg submit failed", "height", v.Height, "err", err)
			continue
		}
		r.Logger.Info("dkg messages submitted", "height", v.Height, "count", len(msgs))
	}
}

// TxSubmitter signs with keyring key Name (SIGN_MODE_DIRECT, zero fee) and broadcasts
// through Client (the node's in-process client).
type TxSubmitter struct {
	Client  client.Context
	Keyring keyring.Keyring
	Name    string
	ChainID string
}

// Submit implements Submitter.
func (s TxSubmitter) Submit(ctx context.Context, msgs []sdk.Msg) error {
	rec, err := s.Keyring.Key(s.Name)
	if err != nil {
		return err
	}
	addr, err := rec.GetAddress()
	if err != nil {
		return err
	}
	cctx := s.Client.WithFromAddress(addr).WithFromName(s.Name).WithKeyring(s.Keyring).WithChainID(s.ChainID)
	f := tx.Factory{}.WithChainID(s.ChainID).WithKeybase(s.Keyring).WithTxConfig(cctx.TxConfig).
		WithAccountRetriever(cctx.AccountRetriever).WithGas(2_000_000).WithSignMode(signing.SignMode_SIGN_MODE_DIRECT)
	if f, err = f.Prepare(cctx); err != nil {
		return err
	}
	txb, err := f.BuildUnsignedTx(msgs...)
	if err != nil {
		return err
	}
	if err := tx.Sign(ctx, f, s.Name, txb, true); err != nil {
		return err
	}
	bz, err := cctx.TxConfig.TxEncoder()(txb.GetTx())
	if err != nil {
		return err
	}
	res, err := cctx.BroadcastTxSync(bz)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("tx rejected (code %d): %s", res.Code, res.RawLog)
	}
	return nil
}
