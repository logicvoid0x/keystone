package secret

import (
	"fmt"
	"path/filepath"
)

// Share is one FROST secret share. Secret is the 32-byte scalar s_i, already negated
// if the group key was normalized to even y (design §9.6); VerifyingShare is s_i·G.
type Share struct {
	Index          uint32 `json:"index"`
	Secret         []byte `json:"secret"`
	VerifyingShare []byte `json:"verifying_share"`
}

// EpochShares is the content of custody/epoch_<n>/shares.json.
type EpochShares struct {
	Epoch    uint64  `json:"epoch"`
	GroupKey []byte  `json:"group_key"`
	N        uint32  `json:"n"`
	T        uint32  `json:"t"`
	Shares   []Share `json:"shares"`
}

// SharesPath is <home>/custody/epoch_<n>/shares.json.
func SharesPath(home string, epoch uint64) string {
	return filepath.Join(home, "custody", fmt.Sprintf("epoch_%d", epoch), "shares.json")
}

func sharesAD(epoch uint64) []byte { return []byte(fmt.Sprintf("keystone/shares/epoch_%d", epoch)) }

// WriteShares encrypts s to SharesPath(home, s.Epoch).
func WriteShares(home string, passphrase []byte, s EpochShares) error {
	return WriteFile(SharesPath(home, s.Epoch), passphrase, s, sharesAD(s.Epoch))
}

// ReadShares decrypts SharesPath(home, epoch).
func ReadShares(home string, passphrase []byte, epoch uint64) (EpochShares, error) {
	var s EpochShares
	err := ReadFile(SharesPath(home, epoch), passphrase, &s, sharesAD(epoch))
	if err == nil && s.Epoch != epoch {
		err = fmt.Errorf("shares file is for epoch %d, want %d", s.Epoch, epoch)
	}
	return s, err
}
