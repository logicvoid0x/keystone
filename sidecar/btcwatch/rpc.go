package btcwatch

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

// RPC is a minimal bitcoind JSON-RPC client implementing Source.
type RPC struct {
	url, user, pass string
	client          *http.Client
	// FixedFeeRate, if positive, is returned by FeeRate instead of estimatesmartfee
	// (regtest has no fee estimates; spec M3-T3 uses 2 sat/vB).
	FixedFeeRate uint64
	// MinFeeRate is used when estimatesmartfee has no estimate.
	MinFeeRate uint64
	id         atomic.Uint64
}

// NewRPC returns a client for the bitcoind at url (e.g. http://127.0.0.1:18443).
func NewRPC(url, user, pass string) *RPC {
	return &RPC{url: url, user: user, pass: pass, client: &http.Client{Timeout: 10 * time.Second}, MinFeeRate: 1}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("bitcoind rpc error %d: %s", e.Code, e.Message) }

// Call invokes method with params and decodes the result into out.
func (r *RPC) Call(ctx context.Context, method string, out any, params ...any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": r.id.Add(1), "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.user != "" {
		req.SetBasicAuth(r.user, r.pass)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s: http %d: %s", method, resp.StatusCode, bytes.TrimSpace(raw[:min(len(raw), 200)]))
	}
	if env.Error != nil {
		return env.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// TipHeight is getblockcount.
func (r *RPC) TipHeight(ctx context.Context) (int64, error) {
	var h int64
	err := r.Call(ctx, "getblockcount", &h)
	return h, err
}

// BlockHash is getblockhash.
func (r *RPC) BlockHash(ctx context.Context, height int64) (chainhash.Hash, error) {
	var s string
	if err := r.Call(ctx, "getblockhash", &s, height); err != nil {
		return chainhash.Hash{}, err
	}
	h, err := chainhash.NewHashFromStr(s)
	if err != nil {
		return chainhash.Hash{}, err
	}
	return *h, nil
}

// Block is getblock <hash> 0 (the raw block, decoded with wire). Verbosity 0 carries
// the same outputs as verbosity 2 and lets wire compute txids.
func (r *RPC) Block(ctx context.Context, hash chainhash.Hash) (*wire.MsgBlock, error) {
	var s string
	if err := r.Call(ctx, "getblock", &s, hash.String(), 0); err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var mb wire.MsgBlock
	if err := mb.Deserialize(bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	return &mb, nil
}

// FeeRate is FixedFeeRate if set, else estimatesmartfee 6 in sat/vB rounded up
// (MinFeeRate when bitcoind has no estimate).
func (r *RPC) FeeRate(ctx context.Context) (uint64, error) {
	if r.FixedFeeRate > 0 {
		return r.FixedFeeRate, nil
	}
	var res struct {
		FeeRate json.Number `json:"feerate"`
		Errors  []string    `json:"errors"`
	}
	if err := r.Call(ctx, "estimatesmartfee", &res, 6); err != nil {
		return 0, err
	}
	if res.FeeRate == "" {
		return r.MinFeeRate, nil
	}
	return BtcPerKvBToSatPerVB(string(res.FeeRate))
}

// BtcPerKvBToSatPerVB converts a decimal BTC/kvB string to sat/vB, rounded up.
func BtcPerKvBToSatPerVB(s string) (uint64, error) {
	rat, ok := new(big.Rat).SetString(s)
	if !ok || rat.Sign() < 0 {
		return 0, fmt.Errorf("bad fee rate %q", s)
	}
	// BTC/kvB × 1e8 sat/BTC ÷ 1000 vB/kvB = × 1e5.
	rat.Mul(rat, big.NewRat(100_000, 1))
	q, m := new(big.Int).QuoRem(rat.Num(), rat.Denom(), new(big.Int))
	if m.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsUint64() {
		return 0, fmt.Errorf("fee rate %q out of range", s)
	}
	return q.Uint64(), nil
}

// Chain is getblockchaininfo's chain ("regtest", "signet", ...).
func (r *RPC) Chain(ctx context.Context) (string, error) {
	var res struct {
		Chain string `json:"chain"`
	}
	err := r.Call(ctx, "getblockchaininfo", &res)
	return res.Chain, err
}

// SendRawTransaction is sendrawtransaction <hex>. A transaction bitcoind already has
// (in the mempool or the chain) is not an error.
func (r *RPC) SendRawTransaction(ctx context.Context, raw []byte) error {
	err := r.Call(ctx, "sendrawtransaction", nil, hex.EncodeToString(raw))
	var re *rpcError
	if errors.As(err, &re) && (re.Code == -27 || strings.Contains(re.Message, "already")) {
		return nil // RPC_VERIFY_ALREADY_IN_UTXO_SET, txn-already-in-mempool / -known
	}
	return err
}
