package signer

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
	"github.com/logicvoid0x/keystone/sidecar/signer/noncestore"
)

func newTestSigner(t *testing.T, v *View) (*Signer, *noncestore.Store, frost.Scalar) {
	t.Helper()
	store, err := noncestore.Open(noncestore.Path(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	var sh frost.Scalar
	sh.SetInt(99)
	s := New(Config{View: viewFunc(func() View { return *v }), Shares: StaticShares{1: {Index: 2, Secret: sh}}, Store: store})
	return s, store, sh
}

type viewFunc func() View

func (f viewFunc) Load(map[uint64]uint32) (View, error) { return f(), nil }

func TestReplenishAndRevoke(t *testing.T) {
	gk := bytes.Repeat([]byte{0}, 33)
	gk[0] = 0x02
	v := &View{NoncePool: 8, Epochs: []EpochView{{Number: 1, GroupKey: gk, Index: 2}}}
	s, store, _ := newTestSigner(t, v)

	// A fresh store forces one revoke; two commitments per VE, persisted first.
	p, err := s.Payload(1)
	require.NoError(t, err)
	require.True(t, p.RevokeAllNonces)
	require.Len(t, p.Commitments, 2)
	require.Equal(t, []uint64{1, 2}, []uint64{p.Commitments[0].Seq, p.Commitments[1].Seq})
	for _, c := range p.Commitments {
		got, ok, err := store.Lookup(1, 2, c.Seq)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, c.Hiding, got.Hiding[:])
	}

	// Not yet recorded: the same seqs and commitments again, no revoke.
	p2, err := s.Payload(2)
	require.NoError(t, err)
	require.False(t, p2.RevokeAllNonces)
	require.Equal(t, p.Commitments, p2.Commitments)

	// Recorded: the next two; the pool is consistent with the store.
	v.Epochs[0].LastRecorded, v.Epochs[0].Pool = 2, p.Commitments
	p3, err := s.Payload(3)
	require.NoError(t, err)
	require.False(t, p3.RevokeAllNonces)
	require.Equal(t, uint64(3), p3.Commitments[0].Seq)

	// A full pool gets nothing new.
	pool := append([]vetypes.NonceCommitment{}, p.Commitments...)
	for len(pool) < 8 {
		pool = append(pool, p3.Commitments[0])
	}
	v.Epochs[0].Pool = pool
	p4, err := s.Payload(4)
	require.NoError(t, err)
	require.Empty(t, p4.Commitments)

	// A commitment on chain that the store does not have (a lost database) → revoke.
	foreign := p.Commitments[0]
	foreign.Seq = 77
	v.Epochs[0].Pool = []vetypes.NonceCommitment{foreign}
	p5, err := s.Payload(5)
	require.NoError(t, err)
	require.True(t, p5.RevokeAllNonces)
}

func TestFileShares(t *testing.T) {
	home := t.TempDir()
	gk, shares, err := secret.GenerateWithDealer(nil, 3, 2, true)
	require.NoError(t, err)
	sb := shares[1].Secret.Bytes()
	y, _ := frost.SerializePoint(shares[1].VerifyingShare)
	pass := []byte("pw")
	require.NoError(t, secret.WriteShares(home, pass, secret.EpochShares{Epoch: 3, GroupKey: gk[:], N: 3, T: 2,
		Shares: []secret.Share{{Index: 2, Secret: sb[:], VerifyingShare: y[:]}}}))
	fs := &FileShares{Home: home, Passphrase: pass}
	epochs, err := fs.Epochs()
	require.NoError(t, err)
	require.Equal(t, []uint64{3}, epochs)
	idx, s, err := fs.Share(3)
	require.NoError(t, err)
	require.Equal(t, uint32(2), idx)
	require.True(t, s.Equals(&shares[1].Secret))
	_, _, err = (&FileShares{Home: home, Passphrase: []byte("wrong")}).Share(3)
	require.Error(t, err)
}
