package cmd

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cast"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/crypto/frost/secret"
	"github.com/OWNER/keystone/sidecar/dkg"
	"github.com/OWNER/keystone/sidecar/signer"
)

const flagPassphraseFile = "passphrase-file"

// custodyCommand is `keystoned custody ...` (spec M4b-T1).
func custodyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "custody", Short: "Custody key management", RunE: client.ValidateCmd}
	cmd.AddCommand(custodyInitCmd(), custodySharesCmd())
	return cmd
}

// custodyInitCmd creates the custody encryption key (<home>/custody/enc_key.json,
// Argon2id + AES-256-GCM) and the custody hot key ("custody" in the keyring), and
// prints {enc_pubkey, hot_key} for genesis or MsgRegisterCustodyKey.
func custodyInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the custody encryption key and the custody hot key",
		Long: "Creates <home>/custody/enc_key.json (encrypted with the passphrase from $" + signer.EnvPassphrase +
			" or --" + flagPassphraseFile + ") and the custody hot key \"" + dkg.HotKeyName + "\" in the keyring. Existing keys are kept.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cctx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			pass := []byte(os.Getenv(signer.EnvPassphrase))
			if f, _ := cmd.Flags().GetString(flagPassphraseFile); len(pass) == 0 && f != "" {
				bz, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				pass = []byte(trimNewline(string(bz)))
			}
			if len(pass) == 0 {
				return fmt.Errorf("set $%s or --%s", signer.EnvPassphrase, flagPassphraseFile)
			}
			if cctx.Keyring == nil {
				return errors.New("no keyring (set --keyring-backend)")
			}
			res, err := dkg.InitCustody(cctx.HomeDir, cctx.Keyring, pass)
			if err != nil {
				return err
			}
			out, err := json.Marshal(map[string]string{"enc_pubkey": hex.EncodeToString(res.EncPubkey), "hot_key": res.HotKey.String()})
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

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// startDKG runs the DKG participant (spec M4b-T2) when the signer is enabled and the
// node has a custody encryption key and an operator address configured. It submits
// its messages with the custody hot key through the in-process client.
func startDKG(svrCtx *server.Context, clientCtx client.Context, ctx context.Context, g *errgroup.Group) error {
	a := runningApp
	opts := svrCtx.Viper
	if a == nil || !cast.ToBool(opts.Get(signer.FlagEnabled)) {
		return nil
	}
	logger := svrCtx.Logger.With("module", "dkg")
	home := svrCtx.Config.RootDir
	pass, err := signer.Passphrase(home, opts)
	if err != nil {
		return err
	}
	enc, err := dkg.LoadEncKey(home, pass)
	if errors.Is(err, os.ErrNotExist) {
		logger.Info("no custody encryption key; the DKG participant is not started (run `keystoned custody init`)")
		return nil
	} else if err != nil {
		return fmt.Errorf("custody encryption key: %w", err)
	}
	op, err := sdk.AccAddressFromBech32(cast.ToString(opts.Get(signer.FlagOperator)))
	if err != nil {
		return fmt.Errorf("%s: %w", signer.FlagOperator, err)
	}
	kb := clientCtx.Keyring
	if kb == nil {
		backend := cast.ToString(opts.Get(flags.FlagKeyringBackend))
		if backend == "" {
			backend = keyring.BackendTest
		}
		if kb, err = keyring.New(sdk.KeyringServiceName(), backend, home, nil, a.AppCodec()); err != nil {
			return err
		}
	}
	rec, err := kb.Key(dkg.HotKeyName)
	if err != nil {
		return fmt.Errorf("custody hot key %q: %w", dkg.HotKeyName, err)
	}
	hot, err := rec.GetAddress()
	if err != nil {
		return err
	}
	if clientCtx.Client == nil {
		return errors.New("the DKG participant needs the in-process client (enable gRPC or the API)")
	}
	r := &dkg.Runner{
		P:      &dkg.Participant{Operator: op, HotKey: hot.String(), EncKey: enc, Store: dkg.FileStore{Home: home, Passphrase: pass}},
		View:   a.DKGView(),
		Submit: dkg.TxSubmitter{Client: clientCtx.WithHomeDir(home), Keyring: kb, Name: dkg.HotKeyName, ChainID: a.ChainID()},
		Logger: logger,
		Poll:   time.Second,
	}
	g.Go(func() error {
		r.Run(ctx)
		return nil
	})
	logger.Info("dkg participant started", "operator", op.String(), "hot_key", hot.String())
	return nil
}

// custodySharesCmd prints the public part of <home>/custody/epoch_<n>/shares.json:
// the group key, the share index and the verifying share (never the secret).
func custodySharesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shares [epoch]",
		Short: "Show the public part of this node's share of a custody epoch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := cmd.Flags().GetString(flags.FlagHome)
			epoch, err := cast.ToUint64E(args[0])
			if err != nil {
				return err
			}
			pass := []byte(os.Getenv(signer.EnvPassphrase))
			if f, _ := cmd.Flags().GetString(flagPassphraseFile); len(pass) == 0 && f != "" {
				bz, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				pass = []byte(trimNewline(string(bz)))
			}
			if len(pass) == 0 {
				return fmt.Errorf("set $%s or --%s", signer.EnvPassphrase, flagPassphraseFile)
			}
			es, err := secret.ReadShares(home, pass, epoch)
			if err != nil {
				return err
			}
			if len(es.Shares) != 1 {
				return fmt.Errorf("%d shares in the file", len(es.Shares))
			}
			out, err := json.Marshal(map[string]any{
				"epoch": es.Epoch, "group_key": hex.EncodeToString(es.GroupKey), "n": es.N, "t": es.T,
				"index": es.Shares[0].Index, "verifying_share": hex.EncodeToString(es.Shares[0].VerifyingShare),
			})
			if err != nil {
				return err
			}
			cmd.Println(string(out))
			return nil
		},
	}
	cmd.Flags().String(flagPassphraseFile, "", "File holding the custody passphrase")
	return cmd
}
