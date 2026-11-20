package noncestore_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/sidecar/signer/noncestore"
)

func share(t *testing.T) frost.Scalar {
	t.Helper()
	var s frost.Scalar
	s.SetInt(12345)
	return s
}

func TestCommitIsStableAndPersisted(t *testing.T) {
	path := noncestore.Path(t.TempDir())
	s, err := noncestore.Open(path)
	require.NoError(t, err)
	require.True(t, s.Fresh, "a missing database is fresh")

	c1, err := s.Commit(1, 2, 7, share(t), nil)
	require.NoError(t, err)
	c2, err := s.Commit(1, 2, 7, share(t), nil)
	require.NoError(t, err)
	require.Equal(t, c1, c2, "the same seq returns the same commitment")
	c3, err := s.Commit(1, 2, 8, share(t), nil)
	require.NoError(t, err)
	require.NotEqual(t, c1.Hiding, c3.Hiding)
	require.NoError(t, s.Close())

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}

	s, err = noncestore.Open(path)
	require.NoError(t, err)
	defer s.Close()
	require.False(t, s.Fresh)
	got, ok, err := s.Lookup(1, 2, 7)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, c1, got)
	c4, err := s.Commit(1, 2, 7, share(t), nil)
	require.NoError(t, err)
	require.Equal(t, c1, c4, "reopened store keeps the nonce")
	_, ok, err = s.Lookup(1, 2, 9)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestBind(t *testing.T) {
	path := noncestore.Path(t.TempDir())
	s, err := noncestore.Open(path)
	require.NoError(t, err)
	c, err := s.Commit(1, 1, 1, share(t), nil)
	require.NoError(t, err)

	_, err = s.Bind(1, 1, 2, []byte("session-a"))
	require.ErrorIs(t, err, noncestore.ErrUnknown)

	n1, err := s.Bind(1, 1, 1, []byte("session-a"))
	require.NoError(t, err)
	d, e, err := n1.Commitment()
	require.NoError(t, err)
	require.Equal(t, c.Hiding, d)
	require.Equal(t, c.Binding, e)
	n2, err := s.Bind(1, 1, 1, []byte("session-a"))
	require.NoError(t, err)
	require.Equal(t, n1, n2, "re-binding to the same session returns the same nonces")
	_, err = s.Bind(1, 1, 1, []byte("session-b"))
	require.ErrorIs(t, err, noncestore.ErrBoundElsewhere)
	require.NoError(t, s.Close())

	// The binding survives a restart.
	s, err = noncestore.Open(path)
	require.NoError(t, err)
	defer s.Close()
	_, err = s.Bind(1, 1, 1, []byte("session-b"))
	require.ErrorIs(t, err, noncestore.ErrBoundElsewhere)
	used, err := s.BoundTo(1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, []byte("session-a"), used)
}

func TestCorruptDatabaseIsReplaced(t *testing.T) {
	home := t.TempDir()
	path := noncestore.Path(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("not a bolt file, definitely corrupt................................................................"), 0o600))
	s, err := noncestore.Open(path)
	require.NoError(t, err)
	require.True(t, s.Fresh)
	_, ok, err := s.Lookup(1, 1, 1)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.Close())
	aside, err := filepath.Glob(path + ".corrupt-*")
	require.NoError(t, err)
	require.Len(t, aside, 1)
}

func TestTamperedRecordIsCorrupt(t *testing.T) {
	path := noncestore.Path(t.TempDir())
	s, err := noncestore.Open(path)
	require.NoError(t, err)
	_, err = s.Commit(1, 1, 1, share(t), nil)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	// Flip a byte of the stored commitment.
	db, err := bolt.Open(path, 0o600, nil)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("nonces"))
		k, v := b.Cursor().First()
		v = append([]byte(nil), v...)
		v[70] ^= 1
		return b.Put(k, v)
	}))
	require.NoError(t, db.Close())

	s, err = noncestore.Open(path)
	require.NoError(t, err)
	defer s.Close()
	require.True(t, s.Fresh)
	_, ok, err := s.Lookup(1, 1, 1)
	require.NoError(t, err)
	require.False(t, ok)
}
