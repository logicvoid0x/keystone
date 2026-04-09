package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmttime "github.com/cometbft/cometbft/types/time"
	"github.com/spf13/cobra"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	kmultisig "github.com/cosmos/cosmos-sdk/crypto/keys/multisig"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/OWNER/keystone/app"
)

const (
	flagOperators  = "operators"
	flagOutput     = "output"
	flagFundAmount = "fund-amount"

	// Port layout for nodes on one machine: node i uses base + 10*i.
	p2pPortBase  = 26656
	rpcPortBase  = 26657
	grpcPortBase = 9090
	apiPortBase  = 1317
	portStride   = 10

	operatorKeyName = "operator"
	testKeyName     = "test"
	adminKeyName    = "admin"
)

// NewTestnetCmd returns `keystoned testnet`.
func NewTestnetCmd(tempApp *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "testnet",
		Short: "Testnet tooling",
	}
	cmd.AddCommand(initFilesCmd(tempApp))
	return cmd
}

// InitFilesArgs configures InitFiles.
type InitFilesArgs struct {
	Operators      int
	OutputDir      string
	ChainID        string
	KeyringBackend string
	FundAmount     sdkmath.Int
}

func initFilesCmd(tempApp *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init-files",
		Short: "Write N node homes with a shared PoA genesis",
		Long: `Writes DIR/node0 .. DIR/node{N-1}, each a keystoned home with an ed25519 consensus key,
an "operator" and a funded "test" account key (keyring backend "test"), and the same genesis:
x/poa operators at power 1, an admin multisig of the operator keys (threshold floor(2N/3)+1;
a single "admin" key in node0 when N = 1), vote extensions enabled at height 1.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			n, _ := cmd.Flags().GetInt(flagOperators)
			out, _ := cmd.Flags().GetString(flagOutput)
			chainID, _ := cmd.Flags().GetString(flags.FlagChainID)
			backend, _ := cmd.Flags().GetString(flags.FlagKeyringBackend)
			fundStr, _ := cmd.Flags().GetString(flagFundAmount)
			fund, ok := sdkmath.NewIntFromString(fundStr)
			if !ok {
				return fmt.Errorf("invalid --%s %q", flagFundAmount, fundStr)
			}
			if err := InitFiles(tempApp.AppCodec(), tempApp.DefaultGenesis(), InitFilesArgs{
				Operators: n, OutputDir: out, ChainID: chainID, KeyringBackend: backend, FundAmount: fund,
			}, bufio.NewReader(cmd.InOrStdin())); err != nil {
				return err
			}
			cmd.PrintErrf("Initialized %d node homes in %s\n", n, out)
			return nil
		},
	}
	cmd.Flags().Int(flagOperators, 4, "number of operators (nodes)")
	cmd.Flags().String(flagOutput, "./localnet/homes", "output directory")
	cmd.Flags().String(flags.FlagChainID, "keystone-local-1", "chain id")
	cmd.Flags().String(flags.FlagKeyringBackend, keyring.BackendTest, "keyring backend")
	cmd.Flags().String(flagFundAmount, "1000000000000", "test-denom balance for each funded account")
	return cmd
}

type nodeFiles struct {
	home     string
	cfg      *cmtcfg.Config
	nodeID   string
	consPub  cryptotypes.PubKey
	kb       keyring.Keyring
	operator *keyring.Record
	test     *keyring.Record
}

// InitFiles writes the node homes and the shared genesis.
func InitFiles(cdc codec.Codec, defaults app.GenesisState, args InitFilesArgs, in *bufio.Reader) error {
	if args.Operators < 1 {
		return errors.New("--operators must be at least 1")
	}
	if args.ChainID == "" {
		return errors.New("--chain-id is required")
	}
	nodes := make([]*nodeFiles, args.Operators)
	for i := range nodes {
		nf, err := initNode(cdc, args, i, in)
		if err != nil {
			return fmt.Errorf("node%d: %w", i, err)
		}
		nodes[i] = nf
	}

	admin, err := createAdmin(nodes)
	if err != nil {
		return err
	}

	input := app.GenesisInput{Admin: admin, FundAmount: args.FundAmount, Accounts: []sdk.AccAddress{admin}}
	for i, nf := range nodes {
		opAddr, err := nf.operator.GetAddress()
		if err != nil {
			return err
		}
		testAddr, err := nf.test.GetAddress()
		if err != nil {
			return err
		}
		input.Operators = append(input.Operators, app.GenesisOperator{
			Address: opAddr, ConsPubKey: nf.consPub.Bytes(), Moniker: fmt.Sprintf("node%d", i),
		})
		input.Accounts = append(input.Accounts, opAddr, testAddr)
	}
	appState, err := app.BuildGenesis(cdc, defaults, input)
	if err != nil {
		return err
	}
	appStateJSON, err := json.MarshalIndent(appState, "", "  ")
	if err != nil {
		return err
	}

	appGenesis := genutiltypes.NewAppGenesisWithVersion(args.ChainID, appStateJSON)
	appGenesis.AppName = app.AppName
	appGenesis.Consensus.Params = app.DefaultConsensusParams()
	genTime := cmttime.Now()

	for i, nf := range nodes {
		var peers []string
		for j, other := range nodes {
			if j != i {
				peers = append(peers, fmt.Sprintf("%s@127.0.0.1:%d", other.nodeID, p2pPortBase+portStride*j))
			}
		}
		nf.cfg.P2P.PersistentPeers = strings.Join(peers, ",")
		cmtcfg.WriteConfigFile(filepath.Join(nf.home, "config", "config.toml"), nf.cfg)

		appCfg := DefaultAppConfig()
		appCfg.GRPC.Address = fmt.Sprintf("127.0.0.1:%d", grpcPortBase+portStride*i)
		appCfg.API.Address = fmt.Sprintf("tcp://127.0.0.1:%d", apiPortBase+portStride*i)
		serverconfig.SetConfigTemplate(serverconfig.DefaultConfigTemplate)
		serverconfig.WriteConfigFile(filepath.Join(nf.home, "config", "app.toml"), appCfg)

		if err := writeClientConfig(nf.home, args.ChainID, args.KeyringBackend, fmt.Sprintf("tcp://127.0.0.1:%d", rpcPortBase+portStride*i)); err != nil {
			return err
		}
		if err := genutil.ExportGenesisFileWithTime(nf.cfg.GenesisFile(), appGenesis, genTime); err != nil {
			return err
		}
	}
	return nil
}

func initNode(cdc codec.Codec, args InitFilesArgs, i int, in *bufio.Reader) (*nodeFiles, error) {
	home := filepath.Join(args.OutputDir, fmt.Sprintf("node%d", i))
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		return nil, err
	}

	cfg := initCometBFTConfig()
	cfg.SetRoot(home)
	cfg.Moniker = fmt.Sprintf("node%d", i)
	cfg.RPC.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", rpcPortBase+portStride*i)
	cfg.P2P.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", p2pPortBase+portStride*i)
	cfg.P2P.AllowDuplicateIP = true
	cfg.P2P.AddrBookStrict = false
	cfg.RPC.PprofListenAddress = ""
	cfg.Instrumentation.Prometheus = false

	// ed25519 consensus key (priv_validator_key.json) and node key.
	nodeID, consPub, err := genutil.InitializeNodeValidatorFilesFromMnemonic(cfg, "")
	if err != nil {
		return nil, err
	}

	kb, err := keyring.New(sdk.KeyringServiceName(), args.KeyringBackend, home, in, cdc)
	if err != nil {
		return nil, err
	}
	op, opMnemonic, err := kb.NewMnemonic(operatorKeyName, keyring.English, sdk.FullFundraiserPath, keyring.DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		return nil, err
	}
	test, testMnemonic, err := kb.NewMnemonic(testKeyName, keyring.English, sdk.FullFundraiserPath, keyring.DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		return nil, err
	}
	seeds, err := json.MarshalIndent(map[string]string{operatorKeyName: opMnemonic, testKeyName: testMnemonic}, "", "  ")
	if err != nil {
		return nil, err
	}
	// Test networks only: the mnemonics let scripts and people recover the keys.
	if err := os.WriteFile(filepath.Join(home, "key_seeds.json"), seeds, 0o600); err != nil {
		return nil, err
	}
	return &nodeFiles{home: home, cfg: cfg, nodeID: nodeID, consPub: consPub, kb: kb, operator: op, test: test}, nil
}

// createAdmin returns the admin address. N = 1: a single "admin" key in node0. N > 1: a
// floor(2N/3)+1 multisig of the operator keys, saved as "admin" in every keyring.
func createAdmin(nodes []*nodeFiles) (sdk.AccAddress, error) {
	if len(nodes) == 1 {
		rec, mnemonic, err := nodes[0].kb.NewMnemonic(adminKeyName, keyring.English, sdk.FullFundraiserPath, keyring.DefaultBIP39Passphrase, hd.Secp256k1)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(nodes[0].home, "admin_seed.json"), []byte(fmt.Sprintf("{\"admin\": %q}\n", mnemonic)), 0o600); err != nil {
			return nil, err
		}
		return rec.GetAddress()
	}
	pubs := make([]cryptotypes.PubKey, len(nodes))
	for i, nf := range nodes {
		pk, err := nf.operator.GetPubKey()
		if err != nil {
			return nil, err
		}
		pubs[i] = pk
	}
	threshold := AdminThreshold(len(nodes))
	multi := kmultisig.NewLegacyAminoPubKey(threshold, pubs)
	for _, nf := range nodes {
		if _, err := nf.kb.SaveMultisig(adminKeyName, multi); err != nil {
			return nil, err
		}
	}
	return sdk.AccAddress(multi.Address()), nil
}

// AdminThreshold is floor(2N/3)+1, strictly more than 2/3 of the operators.
func AdminThreshold(n int) int { return 2*n/3 + 1 }

const clientConfigTemplate = `# This is a TOML config file.
# For more information, see https://github.com/toml-lang/toml

###############################################################################
###                           Client Configuration                            ###
###############################################################################

# The network chain ID
chain-id = "%s"
# The keyring's backend, where the keys are stored (os|file|kwallet|pass|test|memory)
keyring-backend = "%s"
# Default key name, if set, defines the default key to use for signing transaction when the --from flag is not specified
keyring-default-keyname = ""
# CLI output format (text|json)
output = "text"
# <host>:<port> to CometBFT RPC interface for this chain
node = "%s"
# Transaction broadcasting mode (sync|async)
broadcast-mode = "sync"
`

// writeClientConfig writes config/client.toml (same template as SDK v0.55.0
// client/config, whose writer is unexported).
func writeClientConfig(home, chainID, backend, node string) error {
	body := fmt.Sprintf(clientConfigTemplate, chainID, backend, node)
	return os.WriteFile(filepath.Join(home, "config", "client.toml"), []byte(body), 0o644)
}
