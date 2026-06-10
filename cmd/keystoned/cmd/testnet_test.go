package cmd_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	sdkmath "cosmossdk.io/math"

	dbm "github.com/cosmos/cosmos-db"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/cmd/keystoned/cmd"
	"github.com/OWNER/keystone/sidecar/price"
	poatypes "github.com/OWNER/keystone/x/poa/types"
)

func TestAdminThreshold(t *testing.T) {
	for n, want := range map[int]int{1: 1, 2: 2, 3: 3, 4: 3, 7: 5, 20: 14} {
		require.Equal(t, want, cmd.AdminThreshold(n), "n=%d", n)
	}
}

func TestInitFiles(t *testing.T) {
	for _, n := range []int{1, 4} {
		t.Run(fmt.Sprintf("operators=%d", n), func(t *testing.T) {
			a := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
			dir := t.TempDir()
			err := cmd.InitFiles(a.AppCodec(), a.DefaultGenesis(), cmd.InitFilesArgs{
				Operators: n, OutputDir: dir, ChainID: "keystone-local-1",
				KeyringBackend: keyring.BackendTest, FundAmount: sdkmath.NewInt(1000),
			}, bufio.NewReader(strings.NewReader("")))
			require.NoError(t, err)

			var first []byte
			for i := 0; i < n; i++ {
				home := filepath.Join(dir, fmt.Sprintf("node%d", i))
				for _, f := range []string{"genesis.json", "config.toml", "app.toml", "client.toml", "priv_validator_key.json", "node_key.json"} {
					require.FileExists(t, filepath.Join(home, "config", f))
				}
				bz, err := os.ReadFile(filepath.Join(home, "config", "genesis.json"))
				require.NoError(t, err)
				if first == nil {
					first = bz
				}
				require.Equal(t, string(first), string(bz), "every node has the same genesis")

				cfgToml, err := os.ReadFile(filepath.Join(home, "config", "config.toml"))
				require.NoError(t, err)
				require.Contains(t, string(cfgToml), `timeout_commit = "3s"`)
				if n > 1 {
					require.Equal(t, n-1, strings.Count(peersLine(string(cfgToml)), "@"), "persistent peers")
				}
				appToml, err := os.ReadFile(filepath.Join(home, "config", "app.toml"))
				require.NoError(t, err)
				require.Contains(t, string(appToml), `minimum-gas-prices = "0ukusd"`)
				require.Contains(t, string(appToml), `block-executor = "sequential"`)
			}

			gen, err := genutiltypes.AppGenesisFromFile(filepath.Join(dir, "node0", "config", "genesis.json"))
			require.NoError(t, err)
			require.Equal(t, "keystone-local-1", gen.ChainID)
			cp := gen.Consensus.Params
			require.Equal(t, int64(1), cp.ABCI.VoteExtensionsEnableHeight)
			require.Equal(t, int64(4194304), cp.Block.MaxBytes)
			require.Equal(t, int64(100000000), cp.Block.MaxGas)
			require.Equal(t, []string{"ed25519"}, cp.Validator.PubKeyTypes)

			var state map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(gen.AppState, &state))
			require.NoError(t, a.BasicModuleManager.ValidateGenesis(a.AppCodec(), a.TxConfig(), state))

			var poaGen poatypes.GenesisState
			a.AppCodec().MustUnmarshalJSON(state[poatypes.ModuleName], &poaGen)
			require.Len(t, poaGen.Operators, n)
			for _, op := range poaGen.Operators {
				require.Equal(t, int64(1), op.Power)
			}

			kb, err := keyring.New("keystone", keyring.BackendTest, filepath.Join(dir, "node0"), nil, a.AppCodec())
			require.NoError(t, err)
			adminRec, err := kb.Key("admin")
			require.NoError(t, err)
			adminAddr, err := adminRec.GetAddress()
			require.NoError(t, err)
			require.Equal(t, poaGen.Params.Admin, adminAddr.String())
			if n > 1 {
				require.Equal(t, keyring.TypeMulti, adminRec.GetType())
			} else {
				require.Equal(t, keyring.TypeLocal, adminRec.GetType())
			}
		})
	}
}

func peersLine(toml string) string {
	for _, l := range strings.Split(toml, "\n") {
		if strings.HasPrefix(l, "persistent_peers = ") {
			return l
		}
	}
	return ""
}

func TestInitFilesMockfeedSources(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	dir := t.TempDir()
	require.NoError(t, cmd.InitFiles(a.AppCodec(), a.DefaultGenesis(), cmd.InitFilesArgs{
		Operators: 1, OutputDir: dir, ChainID: "keystone-local-1", KeyringBackend: keyring.BackendTest,
		FundAmount: sdkmath.NewInt(1), MockfeedURL: "http://127.0.0.1:18080",
	}, bufio.NewReader(strings.NewReader(""))))

	v := viper.New()
	v.SetConfigFile(filepath.Join(dir, "node0", "config", "app.toml"))
	require.NoError(t, v.ReadInConfig())
	require.Equal(t, []string{
		"mock:http://127.0.0.1:18080/ex/a/btcusd",
		"mock:http://127.0.0.1:18080/ex/b/btcusd",
		"mock:http://127.0.0.1:18080/ex/c/btcusd",
	}, v.GetStringSlice(price.FlagSources))
	require.Equal(t, "1s", v.GetString(price.FlagPollInterval))
	require.Equal(t, 3, v.GetInt(price.FlagMinSources))
	f, err := price.FetcherFromAppOptions(v)
	require.NoError(t, err)
	require.NotNil(t, f)
}

func TestInitFilesDocker(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	dir := t.TempDir()
	require.NoError(t, cmd.InitFiles(a.AppCodec(), a.DefaultGenesis(), cmd.InitFilesArgs{
		Operators: 4, OutputDir: dir, ChainID: "keystone-local-1", KeyringBackend: keyring.BackendTest,
		FundAmount: sdkmath.NewInt(1), Docker: true,
	}, bufio.NewReader(strings.NewReader(""))))
	for i, bps := range []int{0, 10, -15, 25} {
		home := filepath.Join(dir, fmt.Sprintf("node%d", i))
		cfg, err := os.ReadFile(filepath.Join(home, "config", "config.toml"))
		require.NoError(t, err)
		require.Contains(t, string(cfg), `laddr = "tcp://0.0.0.0:26657"`)
		require.Contains(t, string(cfg), `prometheus = true`)
		require.NotContains(t, string(cfg), `\\`, "forward slashes only: Linux containers read these homes")
		require.Contains(t, string(cfg), `prometheus_listen_addr = ":26660"`)
		require.Equal(t, 3, strings.Count(peersLine(string(cfg)), ":26656"))
		require.NotContains(t, peersLine(string(cfg)), fmt.Sprintf("@node%d:", i), "no self peer")

		v := viper.New()
		v.SetConfigFile(filepath.Join(home, "config", "app.toml"))
		require.NoError(t, v.ReadInConfig())
		srcs := v.GetStringSlice(price.FlagSources)
		require.Len(t, srcs, 3)
		require.Equal(t, fmt.Sprintf("mock:http://mockfeed:18080/ex/a/btcusd?offset_bps=%d", bps), srcs[0])
		require.Equal(t, "0.0.0.0:9090", v.GetString("grpc.address"))
	}
}

func TestAddNode(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	dir := t.TempDir()
	require.NoError(t, cmd.InitFiles(a.AppCodec(), a.DefaultGenesis(), cmd.InitFilesArgs{
		Operators: 4, OutputDir: dir, ChainID: "keystone-local-1", KeyringBackend: keyring.BackendTest,
		FundAmount: sdkmath.NewInt(1), Docker: true,
	}, bufio.NewReader(strings.NewReader(""))))
	info, err := cmd.AddNode(a.AppCodec(), cmd.AddNodeArgs{
		InitFilesArgs: cmd.InitFilesArgs{OutputDir: dir, ChainID: "keystone-local-1", KeyringBackend: keyring.BackendTest, Docker: true},
		Index:         4, Peers: "abc@node0:26656", GenesisFile: filepath.Join(dir, "node0", "config", "genesis.json"),
	}, bufio.NewReader(strings.NewReader("")))
	require.NoError(t, err)
	require.Len(t, info.ConsensusPubkey, 32)
	require.Equal(t, "node4", info.Moniker)
	g0, _ := os.ReadFile(filepath.Join(dir, "node0", "config", "genesis.json"))
	g4, _ := os.ReadFile(filepath.Join(dir, "node4", "config", "genesis.json"))
	require.Equal(t, g0, g4)
	cfg, _ := os.ReadFile(filepath.Join(dir, "node4", "config", "config.toml"))
	require.Contains(t, string(cfg), `persistent_peers = "abc@node0:26656"`)
	_, err = cmd.AddNode(a.AppCodec(), cmd.AddNodeArgs{Index: 5}, bufio.NewReader(strings.NewReader("")))
	require.Error(t, err)
}
