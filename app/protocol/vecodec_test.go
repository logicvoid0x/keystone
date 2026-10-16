package protocol_test

import (
	"bytes"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/app/protocol/vetypes"
)

func fullVE(height int64) vetypes.VoteExtension {
	return vetypes.VoteExtension{
		Version: 1,
		Height:  height,
		Oracle:  &vetypes.OraclePayload{PriceMicroUsd: 100_000_000_000, NumSources: 3},
		Bitcoin: &vetypes.BitcoinPayload{
			Anchor:       &vetypes.BtcBlockRef{Height: 100, Hash: bytes.Repeat([]byte{1}, 32)},
			FeerateSatVb: 5,
			Deposits: []vetypes.DepositSeen{
				{Txid: []byte{1}, Vout: 0, AmountSat: 10},
				{Txid: []byte{1}, Vout: 1, AmountSat: 10},
				{Txid: []byte{2}, Vout: 0, AmountSat: 10},
			},
			Confirms: []vetypes.TxConfirmed{{Txid: []byte{1}}, {Txid: []byte{2}}},
			Reorgs:   []vetypes.ReorgSeen{{Txid: []byte{1}, Vout: 0}, {Txid: []byte{1}, Vout: 2}},
			Spends:   []vetypes.UnauthorizedSpend{{PrevTxid: []byte{1}, PrevVout: 0}, {PrevTxid: []byte{1}, PrevVout: 3}},
		},
		Frost: &vetypes.FrostPayload{
			Commitments: []vetypes.NonceCommitment{{ShareIndex: 1, Seq: 0}, {ShareIndex: 1, Seq: 1}, {ShareIndex: 2, Seq: 0}},
			Shares:      []vetypes.CombinedShare{{SessionId: []byte{1}}, {SessionId: []byte{2}}},
		},
	}
}

func mustMarshal(t *testing.T, ve vetypes.VoteExtension) []byte {
	t.Helper()
	bz, err := ve.Marshal()
	require.NoError(t, err)
	return bz
}

func TestDecodeVE(t *testing.T) {
	const h = 7
	cases := []struct {
		name string
		bz   func(t *testing.T) []byte
		ok   bool
	}{
		{"minimal", func(*testing.T) []byte { return protocol.MinimalVE(h) }, true},
		{"canonical full", func(t *testing.T) []byte { return mustMarshal(t, fullVE(h)) }, true},
		{"unsorted deposits", func(t *testing.T) []byte {
			ve := fullVE(h)
			d := ve.Bitcoin.Deposits
			d[0], d[2] = d[2], d[0]
			return mustMarshal(t, ve)
		}, false},
		{"duplicate deposit", func(t *testing.T) []byte {
			ve := fullVE(h)
			ve.Bitcoin.Deposits[1] = ve.Bitcoin.Deposits[0]
			return mustMarshal(t, ve)
		}, false},
		{"unsorted confirms", func(t *testing.T) []byte {
			ve := fullVE(h)
			c := ve.Bitcoin.Confirms
			c[0], c[1] = c[1], c[0]
			return mustMarshal(t, ve)
		}, false},
		{"unsorted reorgs", func(t *testing.T) []byte {
			ve := fullVE(h)
			r := ve.Bitcoin.Reorgs
			r[0], r[1] = r[1], r[0]
			return mustMarshal(t, ve)
		}, false},
		{"unsorted spends", func(t *testing.T) []byte {
			ve := fullVE(h)
			s := ve.Bitcoin.Spends
			s[0], s[1] = s[1], s[0]
			return mustMarshal(t, ve)
		}, false},
		{"too many spends", func(t *testing.T) []byte {
			ve := fullVE(h)
			ve.Bitcoin.Spends = nil
			for i := 0; i <= protocol.MaxSpends; i++ {
				ve.Bitcoin.Spends = append(ve.Bitcoin.Spends, vetypes.UnauthorizedSpend{PrevTxid: []byte{byte(i)}})
			}
			return mustMarshal(t, ve)
		}, false},
		{"unsorted commitments", func(t *testing.T) []byte {
			ve := fullVE(h)
			c := ve.Frost.Commitments
			c[0], c[2] = c[2], c[0]
			return mustMarshal(t, ve)
		}, false},
		{"duplicate share session", func(t *testing.T) []byte {
			ve := fullVE(h)
			ve.Frost.Shares[1].SessionId = ve.Frost.Shares[0].SessionId
			return mustMarshal(t, ve)
		}, false},
		{"too many deposits", func(t *testing.T) []byte {
			ve := fullVE(h)
			ve.Bitcoin.Deposits = nil
			for i := 0; i <= protocol.MaxDeposits; i++ {
				ve.Bitcoin.Deposits = append(ve.Bitcoin.Deposits, vetypes.DepositSeen{Txid: []byte{byte(i)}})
			}
			return mustMarshal(t, ve)
		}, false},
		{"oversize", func(t *testing.T) []byte {
			ve := fullVE(h)
			ve.Bitcoin.Deposits[0].PkScript = make([]byte, protocol.MaxVEBytes)
			return mustMarshal(t, ve)
		}, false},
		{"wrong version", func(t *testing.T) []byte {
			ve := fullVE(h)
			ve.Version = 2
			return mustMarshal(t, ve)
		}, false},
		{"version 0", func(t *testing.T) []byte {
			ve := fullVE(h)
			ve.Version = 0
			return mustMarshal(t, ve)
		}, false},
		{"wrong height", func(t *testing.T) []byte { return protocol.MinimalVE(h + 1) }, false},
		{"garbage", func(*testing.T) []byte { return []byte{0xff, 0xff, 0xff} }, false},
		{"empty", func(*testing.T) []byte { return nil }, false},
		{"non-canonical encoding (unknown field)", func(*testing.T) []byte {
			return append(protocol.MinimalVE(h), 0x50, 0x01) // field 10, varint 1
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := protocol.DecodeVE(tc.bz(t), h)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestCanonicalizeMakesCanonical(t *testing.T) {
	ve := fullVE(3)
	d := ve.Bitcoin.Deposits
	d[0], d[2] = d[2], d[0]
	s := ve.Frost.Shares
	s[0], s[1] = s[1], s[0]
	require.False(t, protocol.IsCanonical(&ve))
	protocol.Canonicalize(&ve)
	require.True(t, protocol.IsCanonical(&ve))
	require.Equal(t, fullVE(3), ve)
}

func TestExtendVoteOutputVerifies(t *testing.T) {
	h := protocol.NewVoteExtHandler()
	ctx := testCtx()
	res, err := h.ExtendVote(ctx, &abci.RequestExtendVote{Height: 9})
	require.NoError(t, err)
	ve, err := protocol.DecodeVE(res.VoteExtension, 9)
	require.NoError(t, err)
	require.Equal(t, vetypes.VoteExtension{Version: 1, Height: 9}, ve)

	v, err := h.VerifyVoteExtension(ctx, &abci.RequestVerifyVoteExtension{Height: 9, VoteExtension: res.VoteExtension})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseVerifyVoteExtension_ACCEPT, v.Status)

	v, err = h.VerifyVoteExtension(ctx, &abci.RequestVerifyVoteExtension{Height: 10, VoteExtension: res.VoteExtension})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseVerifyVoteExtension_REJECT, v.Status, "wrong height")

	v, err = h.VerifyVoteExtension(ctx, &abci.RequestVerifyVoteExtension{Height: 9, VoteExtension: []byte{0xff}})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseVerifyVoteExtension_REJECT, v.Status, "undecodable")
}

type fixedPrice struct {
	p  *vetypes.OraclePayload
	ok bool
}

func (f fixedPrice) Snapshot() (*vetypes.OraclePayload, bool) { return f.p, f.ok }

func TestExtendVoteCarriesPriceSnapshot(t *testing.T) {
	h := protocol.NewVoteExtHandler()
	h.SetPriceSource(fixedPrice{&vetypes.OraclePayload{PriceMicroUsd: 100_000_000_000, NumSources: 3}, true})
	res, err := h.ExtendVote(testCtx(), &abci.RequestExtendVote{Height: 4})
	require.NoError(t, err)
	ve, err := protocol.DecodeVE(res.VoteExtension, 4)
	require.NoError(t, err)
	require.Equal(t, &vetypes.OraclePayload{PriceMicroUsd: 100_000_000_000, NumSources: 3}, ve.Oracle)

	// No snapshot: no oracle payload, still a valid VE.
	h.SetPriceSource(fixedPrice{nil, false})
	res, err = h.ExtendVote(testCtx(), &abci.RequestExtendVote{Height: 4})
	require.NoError(t, err)
	ve, err = protocol.DecodeVE(res.VoteExtension, 4)
	require.NoError(t, err)
	require.Nil(t, ve.Oracle)
}
