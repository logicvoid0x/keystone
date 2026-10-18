package cmd_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"
	sdkmath "cosmossdk.io/math"

	dbm "github.com/cosmos/cosmos-db"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/cmd/keystoned/cmd"
	"github.com/OWNER/keystone/crypto/frost"
	"github.com/OWNER/keystone/crypto/frost/secret"
	"github.com/OWNER/keystone/sidecar/btcwatch"
	"github.com/OWNER/keystone/sidecar/dkg"
	"github.com/OWNER/keystone/sidecar/price"
	"github.com/OWNER/keystone/testutil/devdealer"
	admintypes "github.com/OWNER/keystone/x/admin/types"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
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

			var poaGen admintypes.GenesisState
			a.AppCodec().MustUnmarshalJSON(state[admintypes.ModuleName], &poaGen)
			require.Len(t, poaGen.Allowlist, n)
			var sg stakingtypes.GenesisState
			a.AppCodec().MustUnmarshalJSON(state[stakingtypes.ModuleName], &sg)
			require.Len(t, sg.Validators, n)
			for _, v := range sg.Validators {
				require.Equal(t, app.BondDenom, sg.Params.BondDenom)
				require.True(t, v.Tokens.IsPositive())
			}
			require.Equal(t, cmd.LocalUnbondingTime, sg.Params.UnbondingTime, "localnet unbonding")
			var cg custodytypes.GenesisState
			a.AppCodec().MustUnmarshalJSON(state[custodytypes.ModuleName], &cg)
			require.EqualValues(t, 4, cg.Params.CustodySetSize)
			require.True(t, cg.Params.MinCustodySelfBond.Equal(sdkmath.OneInt()))

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
		require.True(t, v.GetBool(btcwatch.FlagEnabled))
		require.Equal(t, "regtest", v.GetString(btcwatch.FlagNetwork))
		require.Equal(t, fmt.Sprintf("http://bitcoin%d:18443", i), v.GetString(btcwatch.FlagRPCURL))
		require.Equal(t, fmt.Sprintf("tcp://bitcoin%d:28333", i), v.GetString(btcwatch.FlagZMQBlock))
		require.Equal(t, 2, v.GetInt(btcwatch.FlagFixedFeeRate))
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

func TestInitFilesDevDealer(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	args := cmd.InitFilesArgs{
		Operators: 4, OutputDir: t.TempDir(), ChainID: "keystone-signet-1",
		KeyringBackend: keyring.BackendTest, FundAmount: sdkmath.NewInt(1000), DevDealer: true,
	}
	err := cmd.InitFiles(a.AppCodec(), a.DefaultGenesis(), args, bufio.NewReader(strings.NewReader("")))
	require.ErrorContains(t, err, "localnet chain id", "--dev-dealer is refused off localnet")

	args.ChainID = "keystone-local-1"
	require.NoError(t, cmd.InitFiles(a.AppCodec(), a.DefaultGenesis(), args, bufio.NewReader(strings.NewReader(""))))
	gen, err := genutiltypes.AppGenesisFromFile(filepath.Join(args.OutputDir, "node0", "config", "genesis.json"))
	require.NoError(t, err)
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(gen.AppState, &state))
	require.NoError(t, a.BasicModuleManager.ValidateGenesis(a.AppCodec(), a.TxConfig(), state))
	var cg custodytypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(state[custodytypes.ModuleName], &cg)
	require.Equal(t, uint64(1), cg.ActiveEpoch)
	require.Equal(t, uint32(3), cg.Epochs[0].T)
	require.Equal(t, custodytypes.NetworkRegtest, cg.Params.BitcoinNetwork)
	require.Equal(t, uint32(1), cg.Params.ConfirmDepth)

	// Each node holds exactly its own share, encrypted; t of them recover the key.
	shares := map[uint32][]byte{}
	for i := 0; i < 4; i++ {
		home := filepath.Join(args.OutputDir, fmt.Sprintf("node%d", i))
		_, err := secret.ReadShares(home, []byte("wrong"), 1)
		require.Error(t, err)
		s, err := secret.ReadShares(home, []byte(cmd.DefaultCustodyPassphrase), 1)
		require.NoError(t, err)
		require.Len(t, s.Shares, 1)
		require.Equal(t, cg.Epochs[0].GroupKey, s.GroupKey)
		kb, err := keyring.New("keystone", keyring.BackendTest, home, nil, a.AppCodec())
		require.NoError(t, err)
		rec, err := kb.Key("operator")
		require.NoError(t, err)
		opAddr, err := rec.GetAddress()
		require.NoError(t, err)
		idx := slices.IndexFunc(cg.ShareIndices, func(si custodytypes.ShareIndex) bool { return si.Operator == opAddr.String() })
		require.GreaterOrEqual(t, idx, 0)
		require.Equal(t, cg.ShareIndices[idx].Index, s.Shares[0].Index)
		shares[s.Shares[0].Index] = s.Shares[0].Secret

		// The signer is enabled and finds its passphrase (spec M4a-T6).
		toml, err := os.ReadFile(filepath.Join(home, "config", "app.toml"))
		require.NoError(t, err)
		require.Contains(t, string(toml), "[keystone.signer]\n\n# Sign custody sessions with the shares in <home>/custody/epoch_<n>/shares.json.\nenabled = true")
		pw, err := os.ReadFile(filepath.Join(home, cmd.DevPassphraseFile))
		require.NoError(t, err)
		require.Equal(t, cmd.DefaultCustodyPassphrase+"\n", string(pw))
	}
	delete(shares, 2)
	sum, err := devdealer.Combine(shares)
	require.NoError(t, err)
	b := sum.Bytes()
	_, pub := btcec.PrivKeyFromBytes(b[:])
	require.Equal(t, cg.Epochs[0].GroupKey, pub.SerializeCompressed())
}

// Without --dev-dealer, genesis has no custody epoch but every operator's custody keys
// (spec M4b-T1/T3): the encryption key decrypts with the localnet passphrase, the hot
// key is in the keyring and funded, and the signer runs as the operator.
func TestInitFilesCustodyKeys(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{})
	args := cmd.InitFilesArgs{
		Operators: 4, OutputDir: t.TempDir(), ChainID: "keystone-local-1",
		KeyringBackend: keyring.BackendTest, FundAmount: sdkmath.NewInt(1000),
	}
	require.NoError(t, cmd.InitFiles(a.AppCodec(), a.DefaultGenesis(), args, bufio.NewReader(strings.NewReader(""))))
	gen, err := genutiltypes.AppGenesisFromFile(filepath.Join(args.OutputDir, "node0", "config", "genesis.json"))
	require.NoError(t, err)
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(gen.AppState, &state))
	require.NoError(t, a.BasicModuleManager.ValidateGenesis(a.AppCodec(), a.TxConfig(), state))
	var cg custodytypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(state[custodytypes.ModuleName], &cg)
	require.Empty(t, cg.Epochs)
	require.Zero(t, cg.ActiveEpoch)
	require.Len(t, cg.CustodyKeys, 4)
	bank := string(state["bank"])
	for i := 0; i < 4; i++ {
		home := filepath.Join(args.OutputDir, fmt.Sprintf("node%d", i))
		kb, err := keyring.New("keystone", keyring.BackendTest, home, nil, a.AppCodec())
		require.NoError(t, err)
		rec, err := kb.Key("operator")
		require.NoError(t, err)
		op, _ := rec.GetAddress()
		hotRec, err := kb.Key(dkg.HotKeyName)
		require.NoError(t, err)
		hot, _ := hotRec.GetAddress()
		j := slices.IndexFunc(cg.CustodyKeys, func(ck custodytypes.CustodyKey) bool { return ck.Operator == op.String() })
		require.GreaterOrEqual(t, j, 0)
		require.Equal(t, hot.String(), cg.CustodyKeys[j].HotKey)
		require.Contains(t, bank, hot.String(), "the hot key has a genesis account")
		enc, err := dkg.LoadEncKey(home, []byte(cmd.DefaultCustodyPassphrase))
		require.NoError(t, err)
		pub, _ := frost.SerializePoint(frost.MulBase(&enc))
		require.Equal(t, pub[:], cg.CustodyKeys[j].EncPubkey)
		toml, err := os.ReadFile(filepath.Join(home, "config", "app.toml"))
		require.NoError(t, err)
		require.Contains(t, string(toml), `operator = "`+op.String()+`"`)
	}
}
