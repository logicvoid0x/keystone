package dkg

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
)

// FileStore keeps the dealer polynomial in <home>/custody/dkg_<epoch>_<attempt>.json
// and the derived share in epoch_<n>/shares.json, both encrypted with the operator
// passphrase (design §4.4).
type FileStore struct {
	Home       string
	Passphrase []byte
}

func (f FileStore) polyPath(epoch uint64, attempt uint32) string {
	return filepath.Join(f.Home, "custody", fmt.Sprintf("dkg_%d_%d.json", epoch, attempt))
}

func polyAD(epoch uint64, attempt uint32) []byte {
	return []byte(fmt.Sprintf("keystone/dkg-polynomial/%d/%d", epoch, attempt))
}

// LoadPolynomial reads a stored polynomial.
func (f FileStore) LoadPolynomial(epoch uint64, attempt uint32) ([]frost.Scalar, bool, error) {
	var hexes []string
	err := secret.ReadFile(f.polyPath(epoch, attempt), f.Passphrase, &hexes, polyAD(epoch, attempt))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	poly := make([]frost.Scalar, len(hexes))
	for i, h := range hexes {
		b, err := hex.DecodeString(h)
		if err != nil {
			return nil, false, err
		}
		if poly[i], err = frost.ParseScalar(b); err != nil {
			return nil, false, err
		}
	}
	return poly, true, nil
}

// SavePolynomial writes a polynomial (fsynced by secret.WriteFile).
func (f FileStore) SavePolynomial(epoch uint64, attempt uint32, poly []frost.Scalar) error {
	hexes := make([]string, len(poly))
	for i := range poly {
		b := poly[i].Bytes()
		hexes[i] = hex.EncodeToString(b[:])
	}
	return secret.WriteFile(f.polyPath(epoch, attempt), f.Passphrase, hexes, polyAD(epoch, attempt))
}

// DeletePolynomials removes every attempt's polynomial of epoch.
func (f FileStore) DeletePolynomials(epoch uint64) error {
	dir := filepath.Join(f.Home, "custody")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("dkg_%d_", epoch)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// HasShares reports whether epoch_<n>/shares.json exists.
func (f FileStore) HasShares(epoch uint64) (bool, error) {
	_, err := os.Stat(secret.SharesPath(f.Home, epoch))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// WriteShares writes epoch_<n>/shares.json.
func (f FileStore) WriteShares(s secret.EpochShares) error {
	return secret.WriteShares(f.Home, f.Passphrase, s)
}

// MemStore keeps everything in memory (tests and harnesses).
type MemStore struct {
	mu     sync.Mutex
	polys  map[string][]frost.Scalar
	Shares map[uint64]secret.EpochShares
}

func memKey(epoch uint64, attempt uint32) string { return fmt.Sprintf("%d/%d", epoch, attempt) }

// LoadPolynomial implements Store.
func (m *MemStore) LoadPolynomial(epoch uint64, attempt uint32) ([]frost.Scalar, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.polys[memKey(epoch, attempt)]
	return p, ok, nil
}

// SavePolynomial implements Store.
func (m *MemStore) SavePolynomial(epoch uint64, attempt uint32, poly []frost.Scalar) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.polys == nil {
		m.polys = map[string][]frost.Scalar{}
	}
	m.polys[memKey(epoch, attempt)] = poly
	return nil
}

// DeletePolynomials implements Store.
func (m *MemStore) DeletePolynomials(epoch uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.polys {
		if strings.HasPrefix(k, fmt.Sprintf("%d/", epoch)) {
			delete(m.polys, k)
		}
	}
	return nil
}

// HasShares implements Store.
func (m *MemStore) HasShares(epoch uint64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.Shares[epoch]
	return ok, nil
}

// WriteShares implements Store.
func (m *MemStore) WriteShares(s secret.EpochShares) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Shares == nil {
		m.Shares = map[uint64]secret.EpochShares{}
	}
	m.Shares[s.Epoch] = s
	return nil
}
