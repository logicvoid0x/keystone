package signer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
)

// FileShares reads <home>/custody/epoch_<n>/shares.json (Argon2id + AES-256-GCM,
// design §4.4) and caches each decrypted share. Each file holds one share.
type FileShares struct {
	Home       string
	Passphrase []byte

	mu    sync.Mutex
	cache map[uint64]memShare
}

type memShare struct {
	index uint32
	s     frost.Scalar
}

// Epochs lists the epoch_<n> directories that contain a shares.json.
func (f *FileShares) Epochs() ([]uint64, error) {
	dir := filepath.Join(f.Home, "custody")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []uint64
	for _, e := range entries {
		n, ok := strings.CutPrefix(e.Name(), "epoch_")
		if !e.IsDir() || !ok {
			continue
		}
		epoch, err := strconv.ParseUint(n, 10, 64)
		if err != nil {
			continue
		}
		if _, err := os.Stat(secret.SharesPath(f.Home, epoch)); err == nil {
			out = append(out, epoch)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Share decrypts (once) and returns the epoch's share.
func (f *FileShares) Share(epoch uint64) (uint32, frost.Scalar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.cache[epoch]; ok {
		return m.index, m.s, nil
	}
	es, err := secret.ReadShares(f.Home, f.Passphrase, epoch)
	if err != nil {
		return 0, frost.Scalar{}, err
	}
	if len(es.Shares) != 1 {
		return 0, frost.Scalar{}, fmt.Errorf("epoch %d: %d shares in file, want 1", epoch, len(es.Shares))
	}
	sh := es.Shares[0]
	s, err := frost.ParseScalar(sh.Secret)
	if err != nil {
		return 0, frost.Scalar{}, err
	}
	y, err := frost.SerializePoint(frost.MulBase(&s))
	if err != nil || string(y[:]) != string(sh.VerifyingShare) {
		return 0, frost.Scalar{}, fmt.Errorf("epoch %d: share does not match its verifying share", epoch)
	}
	if f.cache == nil {
		f.cache = map[uint64]memShare{}
	}
	f.cache[epoch] = memShare{index: sh.Index, s: s}
	return sh.Index, s, nil
}

// StaticShare is an in-memory share (tests and harnesses).
type StaticShare struct {
	Index  uint32
	Secret frost.Scalar
}

// StaticShares serves shares from memory, keyed by epoch.
type StaticShares map[uint64]StaticShare

// Epochs lists the epochs, ascending.
func (s StaticShares) Epochs() ([]uint64, error) {
	out := make([]uint64, 0, len(s))
	for e := range s {
		out = append(out, e)
	}
	slices.Sort(out)
	return out, nil
}

// Share returns the epoch's share.
func (s StaticShares) Share(epoch uint64) (uint32, frost.Scalar, error) {
	sh, ok := s[epoch]
	if !ok {
		return 0, frost.Scalar{}, fmt.Errorf("no share for epoch %d", epoch)
	}
	return sh.Index, sh.Secret, nil
}

// Delete removes an epoch's share file once the epoch is RETIRED (design §9.4 step 4).
func (f *FileShares) Delete(epoch uint64) error {
	f.mu.Lock()
	delete(f.cache, epoch)
	f.mu.Unlock()
	err := os.Remove(secret.SharesPath(f.Home, epoch))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
