// Package cmd holds the keystoned root command, modelled on SDK v0.55.0
// simapp/simd/cmd.
package cmd

import (
	"os"
	"time"

	cmtcfg "github.com/cometbft/cometbft/config"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/config"
	nodeservice "github.com/cosmos/cosmos-sdk/client/grpc/node"
	"github.com/cosmos/cosmos-sdk/server"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/sidecar/btcwatch"
	"github.com/OWNER/keystone/sidecar/price"
	"github.com/OWNER/keystone/sidecar/signer"
)

// TimeoutCommit targets ~3 s blocks. In CometBFT v0.40 this is config.toml
// [consensus] timeout_commit, not a consensus param (docs/verified-apis.md).
const TimeoutCommit = 3 * time.Second

// NewRootCmd creates the keystoned root command.
func NewRootCmd() *cobra.Command {
	// A temporary app provides the encoding config and autocli options.
	tempApp := app.New(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.NewAppOptionsWithFlagHome(app.DefaultNodeHome))

	initClientCtx := client.Context{}.
		WithCodec(tempApp.AppCodec()).
		WithInterfaceRegistry(tempApp.InterfaceRegistry()).
		WithTxConfig(tempApp.TxConfig()).
		WithLegacyAmino(tempApp.LegacyAmino()).
		WithInput(os.Stdin).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithHomeDir(app.DefaultNodeHome).
		WithViper("")

	rootCmd := &cobra.Command{
		Use:           app.AppName,
		Short:         "Keystone: BTC-backed stablecoin chain",
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SetOut(cmd.OutOrStdout())
			cmd.SetErr(cmd.ErrOrStderr())

			initClientCtx = initClientCtx.WithCmdContext(cmd.Context())
			ctx, err := client.ReadPersistentCommandFlags(initClientCtx, cmd.Flags())
			if err != nil {
				return err
			}
			ctx, err = config.ReadFromClientConfig(ctx)
			if err != nil {
				return err
			}
			if err := client.SetCmdClientContextHandler(ctx, cmd); err != nil {
				return err
			}
			appTemplate, appConfig := initAppConfig()
			return server.InterceptConfigsPreRunHandler(cmd, appTemplate, appConfig, initCometBFTConfig())
		},
	}

	initRootCmd(rootCmd, tempApp)

	autoCliOpts := tempApp.AutoCliOpts()
	autoCliOpts.ClientCtx = initClientCtx
	nodeCmds := nodeservice.NewNodeCommands()
	autoCliOpts.ModuleOptions[nodeCmds.Name()] = nodeCmds.AutoCLIOptions()
	if err := autoCliOpts.EnhanceRootCommand(rootCmd); err != nil {
		panic(err)
	}
	return rootCmd
}

// initCometBFTConfig is the default config.toml for new homes.
func initCometBFTConfig() *cmtcfg.Config {
	cfg := cmtcfg.DefaultConfig()
	cfg.Consensus.TimeoutCommit = TimeoutCommit
	return cfg
}

// KeystoneConfig is the [keystone.*] part of app.toml.
type KeystoneConfig struct {
	Price   price.Config        `mapstructure:"price"`
	Bitcoin btcwatch.FileConfig `mapstructure:"bitcoin"`
	Signer  signer.FileConfig   `mapstructure:"signer"`
}

// AppConfig is app.toml: the SDK server config plus [keystone.*].
type AppConfig struct {
	serverconfig.Config `mapstructure:",squash"`
	Keystone            KeystoneConfig `mapstructure:"keystone"`
}

// AppConfigTemplate is the SDK template plus the Keystone sections.
const AppConfigTemplate = serverconfig.DefaultConfigTemplate + price.ConfigTemplate + btcwatch.ConfigTemplate + signer.ConfigTemplate

// initAppConfig is the default app.toml: zero gas price, sequential executor, no
// app-side mempool (CometBFT FIFO order), real-exchange price sources.
func initAppConfig() (string, any) {
	return AppConfigTemplate, DefaultAppConfig()
}

// DefaultAppConfig returns Keystone's app.toml defaults.
func DefaultAppConfig() *AppConfig {
	cfg := serverconfig.DefaultConfig()
	cfg.MinGasPrices = "0" + app.Denom
	cfg.BlockExecutor = serverconfig.BlockExecutorSequential
	cfg.Mempool.MaxTxs = -1
	return &AppConfig{Config: *cfg, Keystone: KeystoneConfig{Price: price.DefaultConfig(), Bitcoin: btcwatch.DefaultFileConfig(), Signer: signer.DefaultFileConfig()}}
}
