package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

// Deterministic withdrawal transactions (design §9.8 step 3). Pure functions: the
// keeper decides what goes in, these build the bytes.

const (
	// TxVersion, TxLockTime and TxSequence are fixed (§9.8): version 2, locktime 0,
	// every input opts in to RBF.
	TxVersion  = 2
	TxLockTime = 0
	TxSequence = 0xFFFFFFFD

	// ChangeDustSat is the smallest change output (§9.8: change ≥ 330 sat).
	ChangeDustSat = 330
	// RecipientDustSat is the smallest recipient output after its fee share: the
	// P2PKH dust limit, above every accepted script's (docs/decisions.md).
	RecipientDustSat = 546
	// MaxBatchRequests caps the requests in one batch (docs/decisions.md).
	MaxBatchRequests = 64
)

// Recipient script kinds accepted for withdrawals (§9.8 step 1).
const (
	ScriptP2TR   = "p2tr"
	ScriptP2WPKH = "p2wpkh"
	ScriptP2WSH  = "p2wsh"
)

// WithdrawScriptKind classifies a recipient script: P2TR (OP_1 <32>), P2WPKH
// (OP_0 <20>) or P2WSH (OP_0 <32>); anything else is rejected.
func WithdrawScriptKind(s []byte) (string, error) {
	switch {
	case len(s) == 34 && s[0] == txscript.OP_1 && s[1] == txscript.OP_DATA_32:
		return ScriptP2TR, nil
	case len(s) == 22 && s[0] == txscript.OP_0 && s[1] == txscript.OP_DATA_20:
		return ScriptP2WPKH, nil
	case len(s) == 34 && s[0] == txscript.OP_0 && s[1] == txscript.OP_DATA_32:
		return ScriptP2WSH, nil
	default:
		return "", errors.New("recipient script must be P2TR, P2WPKH or P2WSH")
	}
}

// VsizeX2 is twice the §9.8 size estimate 10.5 + 57.5·inputs + 43·outputs vbytes
// (key-path P2TR inputs, worst-case outputs), in integers.
func VsizeX2(inputs, outputs int) uint64 {
	return 21 + 115*uint64(inputs) + 86*uint64(outputs)
}

// FeeFor is ceil(feerate × vsize).
func FeeFor(feerate uint64, inputs, outputs int) uint64 {
	return (feerate*VsizeX2(inputs, outputs) + 1) / 2
}

// FeeShare is a request's share ceil(2 × F × vsize ÷ requests), where vsize counts
// the requests' outputs and a change output.
func FeeShare(feerate uint64, inputs, requests int) uint64 {
	n := feerate * VsizeX2(inputs, requests+1)
	return (n + uint64(requests) - 1) / uint64(requests)
}

// TxInput is a treasury UTXO being spent.
type TxInput struct {
	Txid     []byte // internal byte order
	Vout     uint32
	Amount   uint64
	PkScript []byte // the UTXO's P2TR script
}

// TxOutput is a payment.
type TxOutput struct {
	Script []byte
	Amount uint64
}

// BuildTx builds the unsigned transaction: inputs and outputs in the given order,
// version 2, locktime 0, nSequence 0xFFFFFFFD.
func BuildTx(ins []TxInput, outs []TxOutput) (*wire.MsgTx, error) {
	tx := wire.NewMsgTx(TxVersion)
	tx.LockTime = TxLockTime
	for _, in := range ins {
		h, err := chainhash.NewHash(in.Txid)
		if err != nil {
			return nil, err
		}
		txIn := wire.NewTxIn(wire.NewOutPoint(h, in.Vout), nil, nil)
		txIn.Sequence = TxSequence
		tx.AddTxIn(txIn)
	}
	for _, o := range outs {
		tx.AddTxOut(wire.NewTxOut(int64(o.Amount), bytes.Clone(o.Script)))
	}
	return tx, nil
}

// prevOuts is the BIP-341 prevout fetcher for ins. The map is only read by lookup.
func prevOuts(tx *wire.MsgTx, ins []TxInput) *txscript.MultiPrevOutFetcher {
	m := make(map[wire.OutPoint]*wire.TxOut, len(ins))
	for i, in := range ins {
		m[tx.TxIn[i].PreviousOutPoint] = wire.NewTxOut(int64(in.Amount), in.PkScript)
	}
	return txscript.NewMultiPrevOutFetcher(m)
}

// Sighashes returns the BIP-341 SIGHASH_DEFAULT digest of every input.
func Sighashes(tx *wire.MsgTx, ins []TxInput) ([][]byte, error) {
	if len(tx.TxIn) != len(ins) {
		return nil, errors.New("inputs do not match the transaction")
	}
	fetcher := prevOuts(tx, ins)
	sh := txscript.NewTxSigHashes(tx, fetcher)
	out := make([][]byte, len(ins))
	for i := range ins {
		h, err := txscript.CalcTaprootSignatureHash(sh, txscript.SigHashDefault, tx, i, fetcher)
		if err != nil {
			return nil, fmt.Errorf("sighash input %d: %w", i, err)
		}
		out[i] = h
	}
	return out, nil
}

// SerializeTx encodes tx (with witnesses if any).
func SerializeTx(tx *wire.MsgTx) []byte {
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		panic(err) // writing to a bytes.Buffer cannot fail
	}
	return buf.Bytes()
}

// DeserializeTx decodes a transaction.
func DeserializeTx(bz []byte) (*wire.MsgTx, error) {
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(bz)); err != nil {
		return nil, err
	}
	return &tx, nil
}

// Txid is the transaction id in internal byte order.
func Txid(tx *wire.MsgTx) []byte {
	h := tx.TxHash()
	return bytes.Clone(h[:])
}

// AttachWitnesses sets each input's key-path witness <64-byte signature>
// (SIGHASH_DEFAULT) and returns the signed transaction bytes.
func AttachWitnesses(unsigned []byte, sigs [][]byte) ([]byte, error) {
	tx, err := DeserializeTx(unsigned)
	if err != nil {
		return nil, err
	}
	if len(sigs) != len(tx.TxIn) {
		return nil, errors.New("one signature per input")
	}
	for i, s := range sigs {
		if len(s) != 64 {
			return nil, fmt.Errorf("input %d: signature is %d bytes", i, len(s))
		}
		tx.TxIn[i].Witness = wire.TxWitness{bytes.Clone(s)}
	}
	return SerializeTx(tx), nil
}

// SessionID is SHA-256(chain_id ‖ epoch ‖ txid ‖ input_index ‖ attempt) with epoch as
// 8-byte and input_index and attempt as 4-byte big-endian integers, txid in internal
// byte order (§9.9 rule 4).
func SessionID(chainID string, epoch uint64, txid []byte, input, attempt uint32) []byte {
	h := sha256.New()
	h.Write([]byte(chainID))
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:], epoch)
	h.Write(b[:8])
	h.Write(txid)
	binary.BigEndian.PutUint32(b[8:], input)
	binary.BigEndian.PutUint32(b[12:], attempt)
	h.Write(b[8:16])
	return h.Sum(nil)
}
