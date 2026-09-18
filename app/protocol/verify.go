package protocol

import (
	"bytes"
	"context"

	abci "github.com/cometbft/cometbft/abci/types"
	cryptoenc "github.com/cometbft/cometbft/crypto/encoding"
	cmtprotocrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	protoio "github.com/cosmos/gogoproto/io"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/app/protocol/vetypes"
)

// ValidatorStore resolves consensus keys (x/poa implements it; baseapp.ValidatorStore).
type ValidatorStore interface {
	GetPubKeyByConsAddr(context.Context, sdk.ConsAddress) (cmtprotocrypto.PublicKey, error)
}

// VerifiedVote is a vote whose VE signature verified and whose VE passed the
// structural checks.
type VerifiedVote struct {
	ConsAddr []byte
	Power    int64
	VE       vetypes.VoteExtension
}

// VerifyVotes checks each VE in ext separately and ignores the ones that fail, so one
// bad VE cannot invalidate the commit (design §5.4). It returns the verified votes in
// the order given, their power, and the total power of every vote. height is the vote
// height (block height - 1); VEs must have the version scheduled for it (§17).
// Deterministic: depends only on ext and x/poa state.
func VerifyVotes(ctx context.Context, vs ValidatorStore, chainID string, height int64,
	ext abci.ExtendedCommitInfo, version uint32,
) (votes []VerifiedVote, verified, total int64) {
	for _, v := range ext.Votes {
		total += v.Validator.Power
		if v.BlockIdFlag != cmtproto.BlockIDFlagCommit || len(v.ExtensionSignature) == 0 {
			continue
		}
		pkProto, err := vs.GetPubKeyByConsAddr(ctx, sdk.ConsAddress(v.Validator.Address))
		if err != nil {
			continue // unknown key: ignore this VE
		}
		pk, err := cryptoenc.PubKeyFromProto(pkProto)
		if err != nil || !bytes.Equal(pk.Address(), v.Validator.Address) {
			continue
		}
		sb, err := VoteExtSignBytes(chainID, height, ext.Round, v.VoteExtension)
		if err != nil || !pk.VerifySignature(sb, v.ExtensionSignature) {
			continue
		}
		ve, err := DecodeVEAt(v.VoteExtension, height, version)
		if err != nil {
			continue // signed but malformed: ignore, like a bad signature
		}
		votes = append(votes, VerifiedVote{ConsAddr: v.Validator.Address, Power: v.Validator.Power, VE: ve})
		verified += v.Validator.Power
	}
	return votes, verified, total
}

// VoteExtSignBytes is the message CometBFT signs for a VE: the length-delimited
// CanonicalVoteExtension{extension, height, round, chain_id}, built exactly as in
// baseapp.ValidateVoteExtensions (SDK v0.55.0, gogoproto/io delimited writer).
func VoteExtSignBytes(chainID string, height int64, round int32, ext []byte) ([]byte, error) {
	cve := cmtproto.CanonicalVoteExtension{
		Extension: ext,
		Height:    height,
		Round:     int64(round),
		ChainId:   chainID,
	}
	var buf bytes.Buffer
	if err := protoio.NewDelimitedWriter(&buf).WriteMsg(&cve); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// HasQuorum is the acceptance rule: verified × 3 > total × 2.
func HasQuorum(verified, total int64) bool { return verified*3 > total*2 }
