package protocol_test

import (
	"slices"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/app/protocol"
)

func TestInjectRoundTrip(t *testing.T) {
	keys := testKeys(4)
	ext, _ := commitFor(t, keys, 5, 2, protocol.MinimalVE(5))
	tx := protocol.EncodeInjected(ext)
	require.True(t, protocol.HasMagic(tx))
	require.Equal(t, []byte("KSTN\x01"), tx[:5])

	got, ok := protocol.DecodeInjected(tx)
	require.True(t, ok)
	require.Equal(t, ext, got)

	empty, ok := protocol.DecodeInjected(protocol.EncodeInjected(abci.ExtendedCommitInfo{}))
	require.True(t, ok)
	require.Empty(t, empty.Votes)
}

func TestDecodeInjectedRejects(t *testing.T) {
	tx := protocol.EncodeInjected(abci.ExtendedCommitInfo{Round: 1})
	wrongType := slices.Clone(tx)
	wrongType[4] = 0x02
	cases := map[string][]byte{
		"no magic":     append([]byte("XXXX"), tx[4:]...),
		"magic only":   []byte("KSTN"),
		"wrong type":   wrongType,
		"garbage body": append([]byte("KSTN\x01"), 0xff, 0xff),
		"empty":        nil,
		"short":        []byte("KS"),
	}
	for name, bz := range cases {
		t.Run(name, func(t *testing.T) {
			_, ok := protocol.DecodeInjected(bz)
			require.False(t, ok)
		})
	}
	require.False(t, protocol.HasMagic([]byte("KST")))
	require.True(t, protocol.HasMagic([]byte("KSTNanything")))
}

func TestMatchesLastCommit(t *testing.T) {
	keys := testKeys(4)
	ext, last := commitFor(t, keys, 5, 0, protocol.MinimalVE(5))
	cases := []struct {
		name   string
		mutate func(e *abci.ExtendedCommitInfo, l *abci.CommitInfo)
		ok     bool
	}{
		{"valid", func(*abci.ExtendedCommitInfo, *abci.CommitInfo) {}, true},
		{"round mismatch", func(e *abci.ExtendedCommitInfo, _ *abci.CommitInfo) { e.Round = 1 }, false},
		{"vote count mismatch", func(e *abci.ExtendedCommitInfo, _ *abci.CommitInfo) { e.Votes = e.Votes[:3] }, false},
		{"unsorted", func(e *abci.ExtendedCommitInfo, l *abci.CommitInfo) {
			e.Votes[0], e.Votes[1] = e.Votes[1], e.Votes[0]
			l.Votes[0], l.Votes[1] = l.Votes[1], l.Votes[0]
		}, false},
		{"unsorted by power", func(e *abci.ExtendedCommitInfo, l *abci.CommitInfo) {
			e.Votes[3].Validator.Power = 2
			l.Votes[3].Validator.Power = 2
		}, false},
		{"duplicate", func(e *abci.ExtendedCommitInfo, l *abci.CommitInfo) {
			e.Votes[1] = e.Votes[0]
			l.Votes[1] = l.Votes[0]
		}, false},
		{"address mismatch", func(_ *abci.ExtendedCommitInfo, l *abci.CommitInfo) {
			l.Votes[2].Validator.Address = []byte("different-address-20")
		}, false},
		{"power mismatch", func(_ *abci.ExtendedCommitInfo, l *abci.CommitInfo) { l.Votes[2].Validator.Power = 2 }, false},
		{"block id flag mismatch", func(_ *abci.ExtendedCommitInfo, l *abci.CommitInfo) {
			l.Votes[2].BlockIdFlag = cmtproto.BlockIDFlagAbsent
		}, false},
		{"higher power first is valid", func(e *abci.ExtendedCommitInfo, l *abci.CommitInfo) {
			// Move the last vote to the front with power 2: power desc then address asc.
			v, lv := e.Votes[3], l.Votes[3]
			v.Validator.Power, lv.Validator.Power = 2, 2
			e.Votes = append([]abci.ExtendedVoteInfo{v}, e.Votes[:3]...)
			l.Votes = append([]abci.VoteInfo{lv}, l.Votes[:3]...)
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := cloneExt(ext)
			l := abci.CommitInfo{Round: last.Round, Votes: slices.Clone(last.Votes)}
			tc.mutate(&e, &l)
			require.Equal(t, tc.ok, protocol.MatchesLastCommit(e, l))
		})
	}
}
