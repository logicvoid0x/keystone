package protocol

import (
	"bytes"
	"cmp"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// tx0 layout (design §5.6): Magic | TxTypeInjectedVotes | proto(InjectedVotes).
var Magic = []byte("KSTN")

// TxTypeInjectedVotes is the tx0 type byte.
const TxTypeInjectedVotes = 0x01

// HasMagic reports whether tx starts with the KSTN magic.
func HasMagic(tx []byte) bool { return bytes.HasPrefix(tx, Magic) }

// EncodeInjected builds tx0 from an extended commit, unchanged.
func EncodeInjected(ext abci.ExtendedCommitInfo) []byte {
	body := vetypes.InjectedVotes{ExtendedCommit: ext}
	bz, err := body.Marshal()
	if err != nil {
		// Marshal of a well-formed ExtendedCommitInfo cannot fail.
		panic(err)
	}
	out := make([]byte, 0, len(Magic)+1+len(bz))
	out = append(out, Magic...)
	out = append(out, TxTypeInjectedVotes)
	return append(out, bz...)
}

// DecodeInjected parses tx0. ok is false unless tx has the magic, the type byte and a
// decodable body.
func DecodeInjected(tx []byte) (ext abci.ExtendedCommitInfo, ok bool) {
	if !HasMagic(tx) || len(tx) < len(Magic)+1 || tx[len(Magic)] != TxTypeInjectedVotes {
		return ext, false
	}
	var body vetypes.InjectedVotes
	if err := body.Unmarshal(tx[len(Magic)+1:]); err != nil {
		return ext, false
	}
	return body.ExtendedCommit, true
}

// MatchesLastCommit binds the injected commit to the block's LastCommit (design §5.4).
// It mirrors the SDK v0.55.0 unexported validateExtendedCommitAgainstLastCommit: same
// round; same number of votes; votes sorted by power descending, then address
// ascending; no duplicate addresses; same address and power at each index. Design
// §5.4 also requires the BlockIdFlag at each index to match, which the SDK does not
// check.
func MatchesLastCommit(ext abci.ExtendedCommitInfo, last abci.CommitInfo) bool {
	if ext.Round != last.Round || len(ext.Votes) != len(last.Votes) {
		return false
	}
	for i := 1; i < len(ext.Votes); i++ {
		a, b := ext.Votes[i-1].Validator, ext.Votes[i].Validator
		if c := cmp.Or(cmp.Compare(b.Power, a.Power), bytes.Compare(a.Address, b.Address)); c > 0 {
			return false
		}
	}
	seen := make(map[string]struct{}, len(ext.Votes))
	for i, v := range ext.Votes {
		if _, dup := seen[string(v.Validator.Address)]; dup {
			return false
		}
		seen[string(v.Validator.Address)] = struct{}{}
		lv := last.Votes[i]
		if !bytes.Equal(v.Validator.Address, lv.Validator.Address) || v.Validator.Power != lv.Validator.Power ||
			v.BlockIdFlag != lv.BlockIdFlag {
			return false
		}
	}
	return true
}
