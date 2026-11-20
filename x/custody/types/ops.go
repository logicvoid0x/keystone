package types

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
)

// TallyTTL is the number of blocks after which an unaccepted tally expires (§9.7).
const TallyTTL = 600

// Lengths of Bitcoin hashes in attestations (internal byte order, as in wire).
const HashLen = 32

// Vote is one verified VE's custody payloads (each nil if absent).
type Vote struct {
	ConsAddr []byte
	Bitcoin  *vetypes.BitcoinPayload
	Frost    *vetypes.FrostPayload
}

// FrostInput is a voting operator's FROST payload, recorded by RecordCommitments.
type FrostInput struct {
	Operator sdk.AccAddress
	Payload  vetypes.FrostPayload
}

// ShareAccepted is a combined share that verified (§9.11).
type ShareAccepted struct {
	SessionID []byte
	Validator string
	Z         []byte
}

// SessionSigned is a session whose shares aggregated to a valid signature.
type SessionSigned struct {
	SessionID []byte
	Signature []byte
}

// SessionFailed is a session that failed: Blame sent an invalid share, or Missing did
// not send one by the deadline.
type SessionFailed struct {
	SessionID []byte
	Blame     string
	Missing   []string
}

// OpenBatch asks ApplyOps to build a withdrawal batch at Feerate (§9.8 step 2).
type OpenBatch struct {
	Feerate uint64
}

// CustodyBatch is the result of BuildOps, applied by ApplyOps in the fixed order of
// §9.11: AcceptAnchor, CreditDeposit, RevertDeposit, RecordCommitments,
// ShareAccepted, SessionSigned, SessionFailed (with OpenRetry / SessionStalled),
// ConfirmTx, StartRBF, OpenBatch. Each list is sorted canonically.
type CustodyBatch struct {
	Anchors []vetypes.BtcBlockRef
	Credits []vetypes.DepositSeen
	Reverts []vetypes.ReorgSeen
	// Spends are accepted UnauthorizedSpend attestations (M7, §9.12), by (txid, vout,
	// spending txid).
	Spends []vetypes.UnauthorizedSpend
	// Frost is sorted by operator address bytes.
	Frost []FrostInput
	// Accepted, Signed and Failed are in session id order.
	Accepted []ShareAccepted
	Signed   []SessionSigned
	Failed   []SessionFailed
	// Confirms are accepted TxConfirmed attestations, by txid.
	Confirms []vetypes.TxConfirmed
	// RBF lists batch ids to fee-bump, ascending.
	RBF       []uint64
	OpenBatch *OpenBatch
	// Sweeps are RETIRING epochs to sweep to the active epoch; Consolidate merges the
	// active epoch's smallest UTXOs (M5). Feerate is the block's median feerate.
	Sweeps      []uint64
	Consolidate bool
	Feerate     uint64
	// Voters are the operators whose VE verified this block (signer eligibility,
	// §9.10), sorted by address bytes.
	Voters []sdk.AccAddress
}

// Empty reports whether the batch has nothing to apply. A block with verified voters
// is never empty: stalled sessions are re-checked every block.
func (b CustodyBatch) Empty() bool {
	return len(b.Anchors) == 0 && len(b.Credits) == 0 && len(b.Reverts) == 0 && len(b.Spends) == 0 &&
		len(b.Frost) == 0 && len(b.Accepted) == 0 && len(b.Signed) == 0 && len(b.Failed) == 0 &&
		len(b.Confirms) == 0 && len(b.RBF) == 0 && b.OpenBatch == nil && len(b.Voters) == 0 &&
		len(b.Sweeps) == 0 && !b.Consolidate
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

// ValidConfirm checks a TxConfirmed's shape: 32-byte txid and a block reference.
func ValidConfirm(c *vetypes.TxConfirmed) bool {
	return len(c.Txid) == HashLen && ValidBlockRef(c.Block)
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
	slices.SortFunc(b.Confirms, func(x, y vetypes.TxConfirmed) int {
		return cmp.Or(bytes.Compare(x.Txid, y.Txid), bytes.Compare(x.Block.Hash, y.Block.Hash))
	})
	slices.SortFunc(b.Anchors, func(x, y vetypes.BtcBlockRef) int {
		return cmp.Or(cmp.Compare(x.Height, y.Height), bytes.Compare(x.Hash, y.Hash))
	})
	slices.SortFunc(b.Credits, func(x, y vetypes.DepositSeen) int {
		return cmp.Or(bytes.Compare(x.Txid, y.Txid), cmp.Compare(x.Vout, y.Vout), bytes.Compare(x.Block.Hash, y.Block.Hash))
	})
	slices.SortFunc(b.Spends, func(x, y vetypes.UnauthorizedSpend) int {
		return cmp.Or(bytes.Compare(x.PrevTxid, y.PrevTxid), cmp.Compare(x.PrevVout, y.PrevVout), bytes.Compare(x.SpendingTxid, y.SpendingTxid))
	})
	slices.SortFunc(b.Reverts, func(x, y vetypes.ReorgSeen) int {
		return cmp.Or(bytes.Compare(x.Txid, y.Txid), cmp.Compare(x.Vout, y.Vout), bytes.Compare(x.OldBlockHash, y.OldBlockHash))
	})
}

// ValidSpend checks an UnauthorizedSpend's shape: 32-byte txids and a block reference.
func ValidSpend(s *vetypes.UnauthorizedSpend) bool {
	return len(s.PrevTxid) == HashLen && len(s.SpendingTxid) == HashLen && ValidBlockRef(s.Block)
}
