package types

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/txscript/v2"
)

// Taproot address derivation (design §9.6). Deterministic: used by MsgOpenVault in the
// state machine and by the watcher.

// VaultTag is the BIP-340 tag of the per-vault message m_v.
const VaultTag = "Keystone/vault"

// Bitcoin networks accepted in Params.BitcoinNetwork.
const (
	NetworkRegtest = "regtest"
	NetworkSignet  = "signet"
)

// P2TRScriptLen is the length of OP_1 <32-byte key>.
const P2TRScriptLen = 34

var errTweakOverflow = errors.New("taproot tweak is not a valid scalar")

// NetParams returns the btcd chain params for a network name.
func NetParams(network string) (*chaincfg.Params, error) {
	switch network {
	case NetworkRegtest:
		return &chaincfg.RegressionNetParams, nil
	case NetworkSignet:
		return &chaincfg.SigNetParams, nil
	default:
		return nil, fmt.Errorf("unknown bitcoin network %q", network)
	}
}

// ParseGroupKey parses a 33-byte compressed key and normalizes it to even y: if Y has
// odd y the epoch uses −Y (every share is negated with it, §9.6). It reports whether
// the key was negated.
func ParseGroupKey(bz []byte) (*btcec.PublicKey, bool, error) {
	pk, err := btcec.ParsePubKey(bz)
	if err != nil {
		return nil, false, err
	}
	negated := bz[0] == 0x03
	even, err := schnorr.ParsePubKey(schnorr.SerializePubKey(pk))
	if err != nil {
		return nil, false, err
	}
	return even, negated, nil
}

// VaultMessage is m_v = H_tag("Keystone/vault", chain_id ‖ epoch ‖ vault_id), with
// epoch and vault_id as 8-byte big-endian integers.
func VaultMessage(chainID string, epoch, vaultID uint64) []byte {
	var nums [16]byte
	binary.BigEndian.PutUint64(nums[:8], epoch)
	binary.BigEndian.PutUint64(nums[8:], vaultID)
	return chainhash.TaggedHash([]byte(VaultTag), []byte(chainID), nums[:])[:]
}

// TapTweak is t = H_TapTweak(x(P) ‖ root) (BIP-341). root is empty for the treasury key
// (BIP-86 style, t0) and m_v for vault v (t_v). It fails if t is not below the curve
// order, as BIP-341 requires.
func TapTweak(p *btcec.PublicKey, root []byte) ([]byte, error) {
	t := chainhash.TaggedHash(chainhash.TagTapTweak, schnorr.SerializePubKey(p), root)
	var s btcec.ModNScalar
	if s.SetBytes((*[32]byte)(t)) != 0 {
		return nil, errTweakOverflow
	}
	return t[:], nil
}

// OutputKey is Q = P + t·G for tweak root (see TapTweak).
func OutputKey(p *btcec.PublicKey, root []byte) (*btcec.PublicKey, []byte, error) {
	t, err := TapTweak(p, root)
	if err != nil {
		return nil, nil, err
	}
	return txscript.ComputeTaprootOutputKey(p, root), t, nil
}

// P2TRScript is OP_1 <x(Q)>.
func P2TRScript(q *btcec.PublicKey) []byte {
	return append([]byte{txscript.OP_1, txscript.OP_DATA_32}, schnorr.SerializePubKey(q)...)
}

// IsP2TRScript reports whether script is OP_1 <32 bytes>.
func IsP2TRScript(script []byte) bool {
	return len(script) == P2TRScriptLen && script[0] == txscript.OP_1 && script[1] == txscript.OP_DATA_32
}

// ScriptAddress encodes a P2TR script as a bech32m address for network.
func ScriptAddress(script []byte, network string) (string, error) {
	if !IsP2TRScript(script) {
		return "", errors.New("not a P2TR script")
	}
	params, err := NetParams(network)
	if err != nil {
		return "", err
	}
	a, err := address.NewAddressTaproot(script[2:], params)
	if err != nil {
		return "", err
	}
	return a.EncodeAddress(), nil
}

// VaultScript derives vault v's deposit script and tweak t_v under group key P.
func VaultScript(p *btcec.PublicKey, chainID string, epoch, vaultID uint64) (script, tweak []byte, err error) {
	q, t, err := OutputKey(p, VaultMessage(chainID, epoch, vaultID))
	if err != nil {
		return nil, nil, err
	}
	return P2TRScript(q), t, nil
}

// TreasuryScript derives the treasury / change script and tweak t0 under P.
func TreasuryScript(p *btcec.PublicKey) (script, tweak []byte, err error) {
	q, t, err := OutputKey(p, nil)
	if err != nil {
		return nil, nil, err
	}
	return P2TRScript(q), t, nil
}
