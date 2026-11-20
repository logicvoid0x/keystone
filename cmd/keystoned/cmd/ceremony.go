package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	kmultisig "github.com/cosmos/cosmos-sdk/crypto/keys/multisig"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	sdkmath "cosmossdk.io/math"

	"github.com/logicvoid0x/keystone/app"
	"github.com/logicvoid0x/keystone/sidecar/dkg"
	"github.com/logicvoid0x/keystone/sidecar/signer"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
)

// OperatorInfo is what an operator sends to the genesis ceremony (spec M6-T4): its
// account key (for the admin multisig), consensus key, custody encryption key and
// custody hot key. Only public keys.
type OperatorInfo struct {
	Moniker         string `json:"moniker"`
	OperatorAddress string `json:"operator_address"`
	OperatorPubkey  string `json:"operator_pubkey"`  // base64 compressed secp256k1
	ConsensusPubkey string `json:"consensus_pubkey"` // base64 ed25519
	EncPubkey       string `json:"enc_pubkey"`       // hex compressed secp256k1
	HotKey          string `json:"hot_key"`
}

// CeremonyResult is the genesis and its fingerprint.
type CeremonyResult struct {
	Genesis   *genutiltypes.AppGenesis
	SHA256    string
	Admin     sdk.AccAddress
	Threshold int
}

// Ceremony builds the genesis of a public network from the operators' infos: x/admin
// (admin and allowlist) and x/staking validators,
// every operator bonded with app.DefaultSelfBond (equal power), the admin a ⌊2n/3⌋+1
// multisig of the operator keys
// (5 of 7), the custody keys (no epoch: the DKG runs from height 2, design §9.3), the
// design §10.3 testnet params (signet, k = 6, MembershipDelayBlocks 28,800, cap 10 BTC)
// and accounts for operators and hot keys. Operators are taken in address order, so the
// result does not depend on the order of the inputs.
func Ceremony(cdc codec.Codec, defaults app.GenesisState, infos []OperatorInfo, chainID string, genTime time.Time) (CeremonyResult, error) {
	var res CeremonyResult
	if len(infos) == 0 {
		return res, errors.New("no operators")
	}
	if custodytypes.IsLocalChainID(chainID) {
		return res, fmt.Errorf("%q is a localnet chain id; use testnet init-files", chainID)
	}
	type op struct {
		info OperatorInfo
		addr sdk.AccAddress
		pub  cryptotypes.PubKey
		cons []byte
		enc  []byte
		hot  sdk.AccAddress
	}
	var ops []op
	for _, in := range infos {
		pb, err := base64.StdEncoding.DecodeString(in.OperatorPubkey)
		if err != nil || len(pb) != 33 {
			return res, fmt.Errorf("%s: operator_pubkey must be a base64 33-byte secp256k1 key", in.Moniker)
		}
		pub := &secp256k1.PubKey{Key: pb}
		addr, err := sdk.AccAddressFromBech32(in.OperatorAddress)
		if err != nil || !bytes.Equal(addr, pub.Address()) {
			return res, fmt.Errorf("%s: operator_address does not match operator_pubkey", in.Moniker)
		}
		cons, err := base64.StdEncoding.DecodeString(in.ConsensusPubkey)
		if err != nil || len(cons) != 32 {
			return res, fmt.Errorf("%s: consensus_pubkey must be a base64 32-byte ed25519 key", in.Moniker)
		}
		enc, err := hex.DecodeString(in.EncPubkey)
		if err != nil {
			return res, fmt.Errorf("%s: enc_pubkey: %w", in.Moniker, err)
		}
		hot, err := sdk.AccAddressFromBech32(in.HotKey)
		if err != nil {
			return res, fmt.Errorf("%s: hot_key: %w", in.Moniker, err)
		}
		ops = append(ops, op{info: in, addr: addr, pub: pub, cons: cons, enc: enc, hot: hot})
	}
	slices.SortFunc(ops, func(a, b op) int { return bytes.Compare(a.addr, b.addr) })
	for i := 1; i < len(ops); i++ {
		if ops[i-1].addr.Equals(ops[i].addr) {
			return res, fmt.Errorf("operator %s listed twice", ops[i].addr)
		}
	}

	pubs := make([]cryptotypes.PubKey, len(ops))
	for i, o := range ops {
		pubs[i] = o.pub
	}
	res.Threshold = AdminThreshold(len(ops))
	res.Admin = sdk.AccAddress(kmultisig.NewLegacyAminoPubKey(res.Threshold, pubs).Address())
	in := app.GenesisInput{Admin: res.Admin, FundAmount: sdkmath.ZeroInt(), Accounts: []sdk.AccAddress{res.Admin}}
	for _, o := range ops {
		in.Operators = append(in.Operators, app.GenesisOperator{Address: o.addr, ConsPubKey: o.cons, Moniker: o.info.Moniker})
		in.Accounts = append(in.Accounts, o.addr, o.hot)
	}
	state, err := app.BuildGenesis(cdc, defaults, in)
	if err != nil {
		return res, err
	}
	var cg custodytypes.GenesisState
	if err := cdc.UnmarshalJSON(state[custodytypes.ModuleName], &cg); err != nil {
		return res, err
	}
	cg.Params = custodytypes.DefaultParams() // §10.3 testnet values, BitcoinNetwork signet
	for _, o := range ops {
		cg.CustodyKeys = append(cg.CustodyKeys, custodytypes.CustodyKey{Operator: o.addr.String(), EncPubkey: o.enc, HotKey: o.hot.String()})
	}
	if err := cg.Validate(); err != nil {
		return res, err
	}
	if state[custodytypes.ModuleName], err = cdc.MarshalJSON(&cg); err != nil {
		return res, err
	}
	stateJSON, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return res, err
	}
	g := genutiltypes.NewAppGenesisWithVersion(chainID, stateJSON)
	g.AppName = app.AppName
	g.GenesisTime = genTime.UTC()
	g.Consensus.Params = app.DefaultConsensusParams()
	res.Genesis = g
	return res, nil
}

// operatorInfoCmd prints this node's OperatorInfo, creating the custody keys if needed.
func operatorInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator-info [moniker]",
		Short: "Print this operator's public keys for the genesis ceremony (creates custody keys if missing)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cctx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			if cctx.Keyring == nil {
				return errors.New("no keyring (set --keyring-backend)")
			}
			pass, err := passphraseFromCmd(cmd)
			if err != nil {
				return err
			}
			rec, err := cctx.Keyring.Key(operatorKeyName)
			if err != nil {
				return fmt.Errorf("keyring key %q: %w", operatorKeyName, err)
			}
			pub, err := rec.GetPubKey()
			if err != nil {
				return err
			}
			addr, _ := rec.GetAddress()
			cfg := cmtcfg.DefaultConfig()
			cfg.SetRoot(cctx.HomeDir)
			if _, err := os.Stat(cfg.PrivValidatorKeyFile()); err != nil {
				return fmt.Errorf("consensus key %s: %w (run keystoned init first)", cfg.PrivValidatorKeyFile(), err)
			}
			_, cons, err := genutil.InitializeNodeValidatorFilesFromMnemonic(cfg, "")
			if err != nil {
				return err
			}
			ck, err := dkg.InitCustody(cctx.HomeDir, cctx.Keyring, pass)
			if err != nil {
				return err
			}
			out, err := json.MarshalIndent(OperatorInfo{
				Moniker: args[0], OperatorAddress: addr.String(),
				OperatorPubkey:  base64.StdEncoding.EncodeToString(pub.Bytes()),
				ConsensusPubkey: base64.StdEncoding.EncodeToString(cons.Bytes()),
				EncPubkey:       hex.EncodeToString(ck.EncPubkey), HotKey: ck.HotKey.String(),
			}, "", "  ")
			if err != nil {
				return err
			}
			cmd.Println(string(out))
			return nil
		},
	}
	cmd.Flags().String(flagPassphraseFile, "", "File holding the custody passphrase")
	flags.AddKeyringFlags(cmd.Flags())
	return cmd
}

// ceremonyCmd builds the genesis from operator-info files.
func ceremonyCmd(tempApp *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ceremony [operator-info.json...]",
		Short: "Build a public network's genesis from the operators' operator-info files and print its SHA-256",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chainID, _ := cmd.Flags().GetString(flags.FlagChainID)
			out, _ := cmd.Flags().GetString(flagOutput)
			ts, _ := cmd.Flags().GetString("genesis-time")
			genTime := time.Now()
			if ts != "" {
				t, err := time.Parse(time.RFC3339, ts)
				if err != nil {
					return err
				}
				genTime = t
			}
			var infos []OperatorInfo
			for _, f := range args {
				bz, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				var in OperatorInfo
				if err := json.Unmarshal(bz, &in); err != nil {
					return fmt.Errorf("%s: %w", f, err)
				}
				infos = append(infos, in)
			}
			res, err := Ceremony(tempApp.AppCodec(), tempApp.DefaultGenesis(), infos, chainID, genTime)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := res.Genesis.SaveAs(out); err != nil {
				return err
			}
			bz, err := os.ReadFile(out)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(bz)
			cmd.Printf("genesis %s\nsha256 %x\nadmin %s (%d of %d multisig)\n", out, sum, res.Admin, res.Threshold, len(infos))
			return nil
		},
	}
	cmd.Flags().String(flags.FlagChainID, "", "chain id of the network (not a -local-<n> id)")
	cmd.Flags().String(flagOutput, "genesis.json", "where to write the genesis")
	cmd.Flags().String("genesis-time", "", "genesis time, RFC 3339 (default now)")
	_ = cmd.MarkFlagRequired(flags.FlagChainID)
	return cmd
}

// passphraseFromCmd reads the custody passphrase from $KEYSTONE_CUSTODY_PASSPHRASE or
// --passphrase-file.
func passphraseFromCmd(cmd *cobra.Command) ([]byte, error) {
	pass := []byte(os.Getenv(signer.EnvPassphrase))
	if f, _ := cmd.Flags().GetString(flagPassphraseFile); len(pass) == 0 && f != "" {
		bz, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		pass = []byte(trimNewline(string(bz)))
	}
	if len(pass) == 0 {
		return nil, fmt.Errorf("set $%s or --%s", signer.EnvPassphrase, flagPassphraseFile)
	}
	return pass, nil
}
