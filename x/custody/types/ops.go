package types

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// TallyTTL is the number of blocks after which an unaccepted tally expires (§9.7).
const TallyTTL = 600

// Lengths of Bitcoin hashes in attestations (internal byte order, as in wire).
const HashLen = 32

// BitcoinVote is one verified VE's Bitcoin payload (nil if it had none).
type BitcoinVote struct {
	ConsAddr []byte
	Payload  *vetypes.BitcoinPayload
}

// CustodyBatch is the result of BuildOps, applied by ApplyOps in field order (§5.9):
// AcceptAnchor, CreditDeposit, RevertDeposit. Each list is sorted canonically.
type CustodyBatch struct {
	Anchors []vetypes.BtcBlockRef
	Credits []vetypes.DepositSeen
	Reverts []vetypes.ReorgSeen
}

// Empty reports whether the batch has no operations.
func (b CustodyBatch) Empty() bool {
	return len(b.Anchors) == 0 && len(b.Credits) == 0 && len(b.Reverts) == 0
}

// TallyKey is SHA-256(kind ‖ payload).
func TallyKey(kind AttestationKind, payload []byte) []byte {
	h := sha256.New()
	h.Write([]byte{byte(kind)})
	h.Write(payload)
	return h.Sum(nil)
}

// ValidBlockRef checks an anchor or block reference: a 32-byte hash.
func ValidBlockRef(r *vetypes.BtcBlockRef) bool { return r != nil && len(r.Hash) == HashLen }

// ValidDeposit checks a DepositSeen's shape: 32-byte txid and block hash, a P2TR script,
// a positive amount.
func ValidDeposit(d *vetypes.DepositSeen) bool {
	return len(d.Txid) == HashLen && d.AmountSat > 0 && IsP2TRScript(d.PkScript) && ValidBlockRef(d.Block)
}

// ValidReorg checks a ReorgSeen's shape: 32-byte txid and old block hash.
func ValidReorg(r *vetypes.ReorgSeen) bool {
	return len(r.Txid) == HashLen && len(r.OldBlockHash) == HashLen
}

// TxidString is the conventional (byte-reversed) hex form of an internal-order txid.
func TxidString(txid []byte) string {
	r := slices.Clone(txid)
	slices.Reverse(r)
	return hex.EncodeToString(r)
}

// OutpointString is "txid:vout" with the txid in conventional (reversed) hex.
func OutpointString(txid []byte, vout uint32) string {
	return fmt.Sprintf("%s:%d", TxidString(txid), vout)
}

// Sort puts every list in canonical order: anchors by (height, hash), credits
// and reverts by (txid, vout).
func (b *CustodyBatch) Sort() {
	slices.SortFunc(b.Anchors, func(x, y vetypes.BtcBlockRef) int {
		return cmp.Or(cmp.Compare(x.Height, y.Height), bytes.Compare(x.Hash, y.Hash))
	})
	slices.SortFunc(b.Credits, func(x, y vetypes.DepositSeen) int {
		return cmp.Or(bytes.Compare(x.Txid, y.Txid), cmp.Compare(x.Vout, y.Vout), bytes.Compare(x.Block.Hash, y.Block.Hash))
	})
	slices.SortFunc(b.Reverts, func(x, y vetypes.ReorgSeen) int {
		return cmp.Or(bytes.Compare(x.Txid, y.Txid), cmp.Compare(x.Vout, y.Vout), bytes.Compare(x.OldBlockHash, y.OldBlockHash))
	})
}
