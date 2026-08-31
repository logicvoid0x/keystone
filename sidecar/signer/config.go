package signer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cast"

	"cosmossdk.io/log/v2"

	"github.com/OWNER/keystone/sidecar/signer/noncestore"
)

// app.toml keys under [keystone.signer], the start flag and the passphrase variable.
const (
	FlagEnabled        = "keystone.signer.enabled"
	FlagPassphraseFile = "keystone.signer.passphrase-file"
	FlagOperator       = "keystone.signer.operator"
	// FlagRevokeNonces is the `keystoned start --revoke-nonces` flag (§9.5 restore).
	FlagRevokeNonces = "revoke-nonces"
	// EnvPassphrase, when set, is the share passphrase (it overrides the file).
	EnvPassphrase = "KEYSTONE_CUSTODY_PASSPHRASE"
)

// FileConfig is the [keystone.signer] section of app.toml.
type FileConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// PassphraseFile holds the share passphrase; relative to the node home.
	PassphraseFile string `mapstructure:"passphrase-file"`
	// Operator is this node's x/poa operator address (its DKG identity, M4b).
	Operator string `mapstructure:"operator"`
}

// DefaultFileConfig is disabled.
func DefaultFileConfig() FileConfig { return FileConfig{} }

// ConfigTemplate is appended to the SDK app.toml template.
const ConfigTemplate = `
###############################################################################
###                         Keystone FROST signer                           ###
###############################################################################

[keystone.signer]

# Sign custody sessions with the shares in <home>/custody/epoch_<n>/shares.json.
enabled = {{ .Keystone.Signer.Enabled }}
# File holding the share passphrase, relative to the node home. The environment
# variable KEYSTONE_CUSTODY_PASSPHRASE overrides it.
passphrase-file = "{{ .Keystone.Signer.PassphraseFile }}"
# This node's operator address; the DKG participant runs as this operator with the
# custody hot key "custody" from the keyring and <home>/custody/enc_key.json.
operator = "{{ .Keystone.Signer.Operator }}"
`

// AppOptions is the subset of servertypes.AppOptions used here.
type AppOptions interface{ Get(string) any }

// Passphrase reads the share passphrase from EnvPassphrase or FlagPassphraseFile.
func Passphrase(home string, opts AppOptions) ([]byte, error) {
	pass := []byte(os.Getenv(EnvPassphrase))
	if len(pass) == 0 {
		file := cast.ToString(opts.Get(FlagPassphraseFile))
		if file == "" {
			return nil, fmt.Errorf("set %s or %s", EnvPassphrase, FlagPassphraseFile)
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(home, file)
		}
		bz, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		pass = []byte(strings.TrimRight(string(bz), "\r\n"))
	}
	if len(pass) == 0 {
		return nil, errors.New("empty share passphrase")
	}
	return pass, nil
}

// FromAppOptions opens the nonce store and returns a signer, or nil when disabled.
// The returned close function releases the nonce store.
func FromAppOptions(home string, opts AppOptions, view ChainView, logger log.Logger) (*Signer, func() error, error) {
	if !cast.ToBool(opts.Get(FlagEnabled)) {
		return nil, nil, nil
	}
	pass, err := Passphrase(home, opts)
	if err != nil {
		return nil, nil, err
	}
	store, err := noncestore.Open(noncestore.Path(home))
	if err != nil {
		return nil, nil, err
	}
	if store.Fresh {
		logger.Warn("nonce database was missing or corrupt; a fresh one was created and the next vote extension revokes all unconsumed nonces")
	}
	s := New(Config{
		View: view, Shares: &FileShares{Home: home, Passphrase: pass}, Store: store, Logger: logger,
		RevokeNonces: cast.ToBool(opts.Get(FlagRevokeNonces)),
	})
	return s, store.Close, nil
}
