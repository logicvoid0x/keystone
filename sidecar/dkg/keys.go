package dkg

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
)

// HotKeyName is the keyring name of the custody hot key that signs DKG messages.
const HotKeyName = "custody"

// EncKeyPath is <home>/custody/enc_key.json (design §4.4).
func EncKeyPath(home string) string { return filepath.Join(home, "custody", "enc_key.json") }

var encKeyAD = []byte("keystone/custody-enc-key")

// LoadEncKey decrypts the custody encryption key.
func LoadEncKey(home string, passphrase []byte) (frost.Scalar, error) {
	var h string
	if err := secret.ReadFile(EncKeyPath(home), passphrase, &h, encKeyAD); err != nil {
		return frost.Scalar{}, err
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return frost.Scalar{}, err
	}
	return frost.ParseScalar(b)
}

// Init is the result of InitCustody.
type Init struct {
	EncPubkey []byte         `json:"enc_pubkey"`
	HotKey    sdk.AccAddress `json:"hot_key"`
}

// InitCustody creates (if missing) the secp256k1 custody encryption key, encrypted at
// rest with the passphrase, and the custody hot key in kb (spec M4b-T1). It is
// idempotent: existing keys are kept.
func InitCustody(home string, kb keyring.Keyring, passphrase []byte) (Init, error) {
	var out Init
	k, err := LoadEncKey(home, passphrase)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if k, err = secret.RandomScalar(nil); err != nil {
			return out, err
		}
		b := k.Bytes()
		if err := secret.WriteFile(EncKeyPath(home), passphrase, hex.EncodeToString(b[:]), encKeyAD); err != nil {
			return out, err
		}
	case err != nil:
		return out, err
	}
	pub, err := frost.SerializePoint(frost.MulBase(&k))
	if err != nil {
		return out, err
	}
	out.EncPubkey = pub[:]
	rec, err := kb.Key(HotKeyName)
	if err != nil {
		if rec, _, err = kb.NewMnemonic(HotKeyName, keyring.English, sdk.FullFundraiserPath, keyring.DefaultBIP39Passphrase, hd.Secp256k1); err != nil {
			return out, err
		}
	}
	if out.HotKey, err = rec.GetAddress(); err != nil {
		return out, err
	}
	return out, nil
}
