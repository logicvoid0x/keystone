package cmd

import (
	"context"
	"errors"
	"fmt"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cast"
	"github.com/spf13/cobra"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/debug"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	"github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"

	"github.com/OWNER/keystone/app"
	"github.com/OWNER/keystone/sidecar/btcwatch"
	"github.com/OWNER/keystone/sidecar/price"
	"github.com/OWNER/keystone/sidecar/signer"
)

func initRootCmd(rootCmd *cobra.Command, tempApp *app.App) {
	rootCmd.AddCommand(
		NewTestnetCmd(tempApp),
		genesisCommand(tempApp),
		debugCommand(),
	)

	server.AddCommandsWithStartCmdOptions(rootCmd, app.DefaultNodeHome, newApp, appExport, server.StartCmdOptions{
		AddFlags: func(cmd *cobra.Command) {
			cmd.Flags().Bool(signer.FlagRevokeNonces, false, "Set revoke_all_nonces in the next vote extension (after restoring shares from a backup, design §9.5)")
		},
		PostSetup: startDKG,
	})
	rootCmd.AddCommand(custodyCommand(), genutilcli.InitCmd(tempApp.BasicModuleManager, app.DefaultNodeHome))

	rootCmd.AddCommand(
		server.StatusCommand(),
		queryCommand(),
		txCommand(),
		keys.Commands(),
	)
}

// genesisCommand exposes only `genesis validate`. The genutil gentx/collect flow needs
// x/staking; Keystone genesis comes from `testnet init-files` (docs/decisions.md).
func genesisCommand(tempApp *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "genesis",
		Short:                      "Genesis subcommands",
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(genutilcli.ValidateGenesisCmd(tempApp.BasicModuleManager), operatorInfoCmd(), ceremonyCmd(tempApp))
	return cmd
}

func queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		rpc.WaitTxCmd(),
		server.QueryBlockCmd(),
		authcmd.QueryTxsByEventsCmd(),
		server.QueryBlocksCmd(),
		authcmd.QueryTxCmd(),
		server.QueryBlockResultsCmd(),
	)
	return cmd
}

func txCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		authcmd.GetSignCommand(),
		authcmd.GetSignBatchCommand(),
		authcmd.GetMultiSignCommand(),
		authcmd.GetMultiSignBatchCmd(),
		authcmd.GetValidateSignaturesCommand(),
		authcmd.GetBroadcastCommand(),
		authcmd.GetEncodeCommand(),
		authcmd.GetDecodeCommand(),
		authcmd.GetSimulateCmd(),
		adminUpgradeCmd(),
	)
	return cmd
}

// runningApp is the app newApp created for `keystoned start` (one per process), for
// services started after CometBFT (the DKG participant).
var runningApp *app.App

func newApp(logger log.Logger, db dbm.DB, appOpts servertypes.AppOptions) servertypes.Application {
	a := app.New(logger, db, true, appOpts, server.DefaultBaseappOptions(appOpts)...)
	runningApp = a

	// Prometheus metrics (design §16), served by CometBFT on instrumentation.prometheus_listen_addr.
	if m, err := app.NewMetrics(prometheus.DefaultRegisterer); err != nil {
		logger.Error("keystone metrics not registered", "err", err)
	} else {
		a.EnableMetrics(m)
	}

	// Price sidecar: polls in the background; ExtendVote reads its snapshot.
	fetcher, err := price.FetcherFromAppOptions(appOpts)
	if err != nil {
		panic(fmt.Errorf("[keystone.price]: %w", err))
	}
	if fetcher != nil {
		fetcher.Start(context.Background())
		a.SetPriceSource(price.WithDeadline(fetcher, price.ExtendVoteDeadline))
		logger.Info("price sidecar started")
	} else {
		logger.Warn("no [keystone.price] sources configured; vote extensions carry no price")
	}

	// Bitcoin watcher: follows bitcoind in the background; ExtendVote reads its snapshot.
	w, err := btcwatch.StartFromAppOptions(context.Background(), appOpts, a.BitcoinView(), logger.With("module", "btcwatch"))
	if err != nil {
		panic(fmt.Errorf("[keystone.bitcoin]: %w", err))
	}
	if w != nil {
		a.SetBitcoinSource(w)
		logger.Info("bitcoin watcher started")
	} else {
		logger.Warn("[keystone.bitcoin] disabled; vote extensions carry no Bitcoin attestations")
	}

	// FROST signer: signs custody sessions at ExtendVote with the node's share.
	home := cast.ToString(appOpts.Get(flags.FlagHome))
	s, closeStore, err := signer.FromAppOptions(home, appOpts, a.FrostView(), logger.With("module", "signer"))
	if err != nil {
		panic(fmt.Errorf("[keystone.signer]: %w", err))
	}
	if s != nil {
		slog := logger.With("module", "signer")
		s.SetAlarm(func(err error) {
			slog.Error("FROST SIGNER ALARM: nonce already bound to another session", "err", err)
			if m := a.Metrics(); m != nil {
				m.Full.NonceAlarms.Inc()
			}
		})
		a.SetFrostSource(s)
		a.OnClose(closeStore)
		logger.Info("frost signer started")
	} else {
		logger.Warn("[keystone.signer] disabled; this node signs no custody sessions")
	}
	return a
}

func appExport(
	logger log.Logger, db dbm.DB, height int64, forZeroHeight bool, jailAllowedAddrs []string,
	appOpts servertypes.AppOptions, modulesToExport []string,
) (servertypes.ExportedApp, error) {
	if forZeroHeight {
		return servertypes.ExportedApp{}, errors.New("zero-height export is not supported")
	}
	var a *app.App
	if height != -1 {
		a = app.New(logger, db, false, appOpts)
		if err := a.LoadHeight(height); err != nil {
			return servertypes.ExportedApp{}, err
		}
	} else {
		a = app.New(logger, db, true, appOpts)
	}
	return a.ExportAppStateAndValidators(forZeroHeight, jailAllowedAddrs, modulesToExport)
}

// debugCommand is the SDK debug command plus decode-tx0.
func debugCommand() *cobra.Command {
	cmd := debug.Cmd()
	cmd.AddCommand(decodeTx0Cmd())
	return cmd
}
