// Package app wires the Keystone application: SDK auth, bank, consensus, upgrade,
// genutil and (M7) staking, slashing, distribution, mint and evidence, plus x/admin and
// the five Keystone modules. Started from SDK v0.55.0 simapp/app.go with gov,
// feegrant, authz, epochs and params removed.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cast"

	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"
	reflectionv1 "cosmossdk.io/api/cosmos/reflection/v1"
	"cosmossdk.io/client/v2/autocli"
	clienthelpers "cosmossdk.io/client/v2/helpers"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	nodeservice "github.com/cosmos/cosmos-sdk/client/grpc/node"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	runtimeservices "github.com/cosmos/cosmos-sdk/runtime/services"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/api"
	"github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/std"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authcodec "github.com/cosmos/cosmos-sdk/x/auth/codec"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	"github.com/cosmos/cosmos-sdk/x/auth/posthandler"
	authsims "github.com/cosmos/cosmos-sdk/x/auth/simulation"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	consensuskeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	"github.com/cosmos/cosmos-sdk/x/distribution"
	distrkeeper "github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/cosmos/cosmos-sdk/x/evidence"
	evidencekeeper "github.com/cosmos/cosmos-sdk/x/evidence/keeper"
	evidencetypes "github.com/cosmos/cosmos-sdk/x/evidence/types"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/cosmos/cosmos-sdk/x/mint"
	mintkeeper "github.com/cosmos/cosmos-sdk/x/mint/keeper"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	slashingkeeper "github.com/cosmos/cosmos-sdk/x/slashing/keeper"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/cosmos-sdk/x/tx/signing"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	upgradekeeper "github.com/cosmos/cosmos-sdk/x/upgrade/keeper"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	"github.com/OWNER/keystone/app/protocol"
	"github.com/OWNER/keystone/app/valset"
	"github.com/OWNER/keystone/x/admin"
	adminkeeper "github.com/OWNER/keystone/x/admin/keeper"
	admintypes "github.com/OWNER/keystone/x/admin/types"
	"github.com/OWNER/keystone/x/custody"
	custodykeeper "github.com/OWNER/keystone/x/custody/keeper"
	custodytypes "github.com/OWNER/keystone/x/custody/types"
	"github.com/OWNER/keystone/x/liquidation"
	liquidationkeeper "github.com/OWNER/keystone/x/liquidation/keeper"
	liquidationtypes "github.com/OWNER/keystone/x/liquidation/types"
	"github.com/OWNER/keystone/x/oracle"
	oraclekeeper "github.com/OWNER/keystone/x/oracle/keeper"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
	"github.com/OWNER/keystone/x/stablecoin"
	stablecoinkeeper "github.com/OWNER/keystone/x/stablecoin/keeper"
	stablecointypes "github.com/OWNER/keystone/x/stablecoin/types"
	"github.com/OWNER/keystone/x/vault"
	vaultkeeper "github.com/OWNER/keystone/x/vault/keeper"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

// Module account names created in M0 and used from M1.
const (
	StablecoinModuleAccount  = stablecointypes.ModuleName
	SurplusModuleAccount     = stablecointypes.SurplusModuleAccount
	LiquidationModuleAccount = liquidationtypes.ModuleName
)

var (
	// DefaultNodeHome is the default home directory for keystoned.
	DefaultNodeHome string

	maccPerms = map[string][]string{
		// Gas is zero (design §7.7); the fee collector receives x/mint inflation and the
		// stakers' KUSD share, which x/distribution pays out.
		authtypes.FeeCollectorName:          nil,
		StablecoinModuleAccount:             {authtypes.Minter, authtypes.Burner},
		SurplusModuleAccount:                nil,
		LiquidationModuleAccount:            {authtypes.Burner},
		distrtypes.ModuleName:               nil,
		minttypes.ModuleName:                {authtypes.Minter},
		stakingtypes.BondedPoolName:         {authtypes.Burner, authtypes.Staking},
		stakingtypes.NotBondedPoolName:      {authtypes.Burner, authtypes.Staking},
		stakingtypes.KeyRotationFeePoolName: {authtypes.Burner},
	}
)

var _ servertypes.Application = (*App)(nil)

func init() {
	SetAddressConfig()
	var err error
	DefaultNodeHome, err = clienthelpers.GetNodeHomeDirectory(".keystoned")
	if err != nil {
		panic(err)
	}
}

// App is the Keystone application.
type App struct {
	*baseapp.BaseApp
	legacyAmino       *codec.LegacyAmino
	appCodec          codec.Codec
	txConfig          client.TxConfig
	interfaceRegistry types.InterfaceRegistry

	keys  map[string]*storetypes.KVStoreKey
	tkeys map[string]*storetypes.TransientStoreKey

	AccountKeeper         authkeeper.AccountKeeper
	BankKeeper            bankkeeper.BaseKeeper
	UpgradeKeeper         *upgradekeeper.Keeper
	ConsensusParamsKeeper consensuskeeper.Keeper

	StakingKeeper  *stakingkeeper.Keeper
	SlashingKeeper slashingkeeper.Keeper
	DistrKeeper    distrkeeper.Keeper
	MintKeeper     mintkeeper.Keeper
	EvidenceKeeper evidencekeeper.Keeper

	AdminKeeper       *adminkeeper.Keeper
	OracleKeeper      *oraclekeeper.Keeper
	VaultKeeper       *vaultkeeper.Keeper
	StablecoinKeeper  *stablecoinkeeper.Keeper
	LiquidationKeeper *liquidationkeeper.Keeper
	CustodyKeeper     *custodykeeper.Keeper

	ModuleManager      *module.Manager
	BasicModuleManager module.BasicManager
	configurator       module.Configurator

	protocol  *protocol.PreBlocker
	voteExt   *protocol.VoteExtHandler
	price     protocol.PriceSource
	closers   []func() error
	bitcoin   protocol.BitcoinSource
	invState  invariantState
	metrics   *Metrics
	proposals *protocol.ProposalHandler
}

// New returns a Keystone app. It refuses to start with any block executor other than
// "sequential" (CLAUDE.md: never BlockSTM) and never enables optimistic execution.
func New(
	logger log.Logger,
	db dbm.DB,
	loadLatest bool,
	appOpts servertypes.AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) *App {
	if ex := cast.ToString(appOpts.Get(server.FlagBlockExecutor)); ex != "" && ex != config.BlockExecutorSequential {
		panic(fmt.Errorf("keystoned requires block-executor = %q, got %q", config.BlockExecutorSequential, ex))
	}

	interfaceRegistry, err := types.NewInterfaceRegistryWithOptions(types.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.Bech32Codec{Bech32Prefix: AccountAddressPrefix},
			ValidatorAddressCodec: address.Bech32Codec{Bech32Prefix: ValidatorAddressPrefix},
		},
	})
	if err != nil {
		panic(err)
	}
	appCodec := codec.NewProtoCodec(interfaceRegistry)
	legacyAmino := codec.NewLegacyAmino()
	txConfig := authtx.NewTxConfig(appCodec, authtx.DefaultSignModes)

	std.RegisterLegacyAminoCodec(legacyAmino)
	std.RegisterInterfaces(interfaceRegistry)

	bApp := baseapp.NewBaseApp(AppName, logger, db, txConfig.TxDecoder(), baseAppOptions...)
	bApp.SetVersion(version.Version)
	bApp.SetInterfaceRegistry(interfaceRegistry)
	bApp.SetTxEncoder(txConfig.TxEncoder())

	keys := storetypes.NewKVStoreKeys(
		authtypes.StoreKey, banktypes.StoreKey, consensustypes.StoreKey, upgradetypes.StoreKey,
		stakingtypes.StoreKey, slashingtypes.StoreKey, distrtypes.StoreKey, minttypes.StoreKey,
		evidencetypes.StoreKey,
		admintypes.StoreKey, oracletypes.StoreKey, vaulttypes.StoreKey, stablecointypes.StoreKey,
		liquidationtypes.StoreKey, custodytypes.StoreKey,
	)
	tkeys := storetypes.NewTransientStoreKeys(TxCountStoreKey)

	if err := bApp.RegisterStreamingServices(appOpts, keys); err != nil {
		panic(err)
	}

	app := &App{
		BaseApp:           bApp,
		legacyAmino:       legacyAmino,
		appCodec:          appCodec,
		txConfig:          txConfig,
		interfaceRegistry: interfaceRegistry,
		keys:              keys,
		tkeys:             tkeys,
	}

	// Authority for SDK keepers: the x/admin module address. Nobody can sign for it; the
	// upgrade and consensus Msg services are re-gated to the x/admin admin
	// (authority.go), and the other SDK MsgUpdateParams go through x/admin MsgExec.
	moduleAuthority := adminkeeper.ModuleAddress().String()

	app.ConsensusParamsKeeper = consensuskeeper.NewKeeper(
		appCodec, runtime.NewKVStoreService(keys[consensustypes.StoreKey]), moduleAuthority, runtime.EventService{},
	)
	bApp.SetParamStore(app.ConsensusParamsKeeper.ParamsStore)

	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec, runtime.NewKVStoreService(keys[authtypes.StoreKey]), authtypes.ProtoBaseAccount,
		maccPerms, authcodec.NewBech32Codec(AccountAddressPrefix), AccountAddressPrefix, moduleAuthority,
	)
	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec, runtime.NewKVStoreService(keys[banktypes.StoreKey]), app.AccountKeeper,
		BlockedAddresses(), moduleAuthority, logger,
	)

	skipUpgradeHeights := map[int64]bool{}
	for _, h := range cast.ToIntSlice(appOpts.Get(server.FlagUnsafeSkipUpgrades)) {
		skipUpgradeHeights[int64(h)] = true
	}
	app.UpgradeKeeper = upgradekeeper.NewKeeper(
		skipUpgradeHeights, runtime.NewKVStoreService(keys[upgradetypes.StoreKey]), appCodec,
		cast.ToString(appOpts.Get(flags.FlagHome)), app.BaseApp, moduleAuthority,
	)

	// Proof of stake (design §4.2, M7): stock SDK modules with the admin module address
	// as authority.
	app.StakingKeeper = stakingkeeper.NewKeeper(
		appCodec, runtime.NewKVStoreService(keys[stakingtypes.StoreKey]), app.AccountKeeper, app.BankKeeper, moduleAuthority,
		authcodec.NewBech32Codec(ValidatorAddressPrefix), authcodec.NewBech32Codec(ConsensusAddressPrefix),
	)
	app.MintKeeper = mintkeeper.NewKeeper(
		appCodec, runtime.NewKVStoreService(keys[minttypes.StoreKey]), app.StakingKeeper, app.AccountKeeper, app.BankKeeper,
		authtypes.FeeCollectorName, moduleAuthority,
	)
	app.DistrKeeper = distrkeeper.NewKeeper(
		appCodec, runtime.NewKVStoreService(keys[distrtypes.StoreKey]), app.AccountKeeper, app.BankKeeper, app.StakingKeeper,
		authtypes.FeeCollectorName, moduleAuthority,
	)
	app.SlashingKeeper = slashingkeeper.NewKeeper(
		appCodec, legacyAmino, runtime.NewKVStoreService(keys[slashingtypes.StoreKey]), app.StakingKeeper, moduleAuthority,
	)
	app.StakingKeeper.SetHooks(stakingtypes.NewMultiStakingHooks(app.DistrKeeper.Hooks(), app.SlashingKeeper.Hooks()))
	app.EvidenceKeeper = *evidencekeeper.NewKeeper(
		appCodec, runtime.NewKVStoreService(keys[evidencetypes.StoreKey]), app.StakingKeeper, app.SlashingKeeper,
		app.AccountKeeper.AddressCodec(), runtime.ProvideCometInfoService(),
	)

	app.AdminKeeper = adminkeeper.NewKeeper(appCodec, runtime.NewKVStoreService(keys[admintypes.StoreKey]), app.StakingKeeper)
	vals := valset.New(app.StakingKeeper)
	app.OracleKeeper = oraclekeeper.NewKeeper(appCodec, runtime.NewKVStoreService(keys[oracletypes.StoreKey]), app.AdminKeeper, vals)
	app.StablecoinKeeper = stablecoinkeeper.NewKeeper(appCodec, runtime.NewKVStoreService(keys[stablecointypes.StoreKey]), app.AdminKeeper, app.BankKeeper)
	app.CustodyKeeper = custodykeeper.NewKeeper(appCodec, runtime.NewKVStoreService(keys[custodytypes.StoreKey]), app.AdminKeeper, vals)
	app.VaultKeeper = vaultkeeper.NewKeeper(appCodec, runtime.NewKVStoreService(keys[vaulttypes.StoreKey]), app.AdminKeeper,
		app.OracleKeeper, app.StablecoinKeeper, app.CustodyKeeper)
	app.CustodyKeeper.SetVaultKeeper(app.VaultKeeper)
	// x/admin asks x/custody before every allowlist removal (design §9.4, M7-T2).
	app.AdminKeeper.SetRemovalGuard(app.CustodyKeeper.RemovalGuard())
	app.LiquidationKeeper = liquidationkeeper.NewKeeper(appCodec, runtime.NewKVStoreService(keys[liquidationtypes.StoreKey]), app.AdminKeeper,
		app.OracleKeeper, app.VaultKeeper, app.StablecoinKeeper, app.CustodyKeeper, app.BankKeeper)

	app.ModuleManager = module.NewManager(
		genutil.NewAppModule(app.AccountKeeper, app.StakingKeeper, app, txConfig),
		auth.NewAppModule(appCodec, app.AccountKeeper, authsims.RandomGenesisAccounts),
		bank.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper),
		adminUpgradeModule{
			AppModule: upgrade.NewAppModule(app.UpgradeKeeper, app.AccountKeeper.AddressCodec()),
			keeper:    app.UpgradeKeeper, admin: app.AdminKeeper, authority: moduleAuthority,
		},
		adminConsensusModule{
			AppModule: consensus.NewAppModule(appCodec, app.ConsensusParamsKeeper),
			keeper:    app.ConsensusParamsKeeper, admin: app.AdminKeeper, authority: moduleAuthority,
		},
		staking.NewAppModule(appCodec, app.StakingKeeper, app.AccountKeeper, app.BankKeeper),
		slashing.NewAppModule(appCodec, app.SlashingKeeper, app.AccountKeeper, app.BankKeeper, app.StakingKeeper, interfaceRegistry),
		distribution.NewAppModule(appCodec, app.DistrKeeper, app.AccountKeeper, app.BankKeeper, app.StakingKeeper),
		mint.NewAppModule(appCodec, app.MintKeeper, app.AccountKeeper, nil),
		evidence.NewAppModule(app.EvidenceKeeper),
		admin.NewAppModule(appCodec, app.AdminKeeper),
		oracle.NewAppModule(appCodec, app.OracleKeeper),
		vault.NewAppModule(appCodec, app.VaultKeeper),
		stablecoin.NewAppModule(appCodec, app.StablecoinKeeper),
		liquidation.NewAppModule(appCodec, app.LiquidationKeeper),
		custody.NewAppModule(appCodec, app.CustodyKeeper),
	)

	app.BasicModuleManager = module.NewBasicManagerFromManager(app.ModuleManager, map[string]module.AppModuleBasic{
		genutiltypes.ModuleName: genutil.NewAppModuleBasic(genutiltypes.DefaultMessageValidator),
	})
	app.BasicModuleManager.RegisterLegacyAminoCodec(legacyAmino)
	app.BasicModuleManager.RegisterInterfaces(interfaceRegistry)

	if err := interfaceRegistry.SigningContext().Validate(); err != nil {
		panic(err)
	}

	// x/upgrade must run first (design §17); the protocol PreBlock follows in PreBlocker.
	app.ModuleManager.SetOrderPreBlockers(upgradetypes.ModuleName, authtypes.ModuleName)
	app.ModuleManager.SetOrderBeginBlockers(
		minttypes.ModuleName, distrtypes.ModuleName, slashingtypes.ModuleName, evidencetypes.ModuleName,
		stakingtypes.ModuleName,
	)
	// x/staking ends the block: it alone returns validator updates, and custody reads
	// the set last sent to CometBFT before it changes (design §9.2).
	app.ModuleManager.SetOrderEndBlockers(
		banktypes.ModuleName, stablecointypes.ModuleName, custodytypes.ModuleName, stakingtypes.ModuleName,
	)
	// distribution precedes staking (staking's genesis hooks write distribution
	// records); custody follows staking (it reads the validator set).
	genesisOrder := []string{
		authtypes.ModuleName, banktypes.ModuleName, distrtypes.ModuleName, stakingtypes.ModuleName,
		slashingtypes.ModuleName, minttypes.ModuleName, evidencetypes.ModuleName, admintypes.ModuleName,
		oracletypes.ModuleName, stablecointypes.ModuleName, vaulttypes.ModuleName, liquidationtypes.ModuleName,
		custodytypes.ModuleName, genutiltypes.ModuleName, upgradetypes.ModuleName,
		consensustypes.ModuleName,
	}
	app.ModuleManager.SetOrderInitGenesis(genesisOrder...)
	app.ModuleManager.SetOrderExportGenesis(genesisOrder...)

	app.configurator = module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter())
	if err := app.ModuleManager.RegisterServices(app.configurator); err != nil {
		panic(err)
	}
	app.AdminKeeper.SetExecRouter(app.MsgServiceRouter(), AdminExecTypes())

	autocliv1.RegisterQueryServer(app.GRPCQueryRouter(), runtimeservices.NewAutoCLIQueryService(app.ModuleManager.Modules))
	reflectionSvc, err := runtimeservices.NewReflectionService()
	if err != nil {
		panic(err)
	}
	reflectionv1.RegisterReflectionServiceServer(app.GRPCQueryRouter(), reflectionSvc)

	app.MountKVStores(keys)
	app.MountTransientStores(tkeys)

	app.SetInitChainer(app.InitChainer)
	app.SetPreBlocker(app.PreBlocker)
	app.SetBeginBlocker(app.BeginBlocker)
	app.SetEndBlocker(app.EndBlocker)
	app.setAnteHandler()
	app.setPostHandler()
	app.setABCIHandlers()
	// Tests that simulate the pre-upgrade binary defer the handlers
	// (RegisterUpgradeHandlers); a node always registers them.
	if !cast.ToBool(appOpts.Get(FlagDeferUpgradeHandlers)) {
		app.registerUpgrades()
	}

	if loadLatest {
		if err := app.LoadLatestVersion(); err != nil {
			panic(fmt.Errorf("error loading last version: %w", err))
		}
	}
	return app
}

func (app *App) setAnteHandler() {
	h, err := NewAnteHandler(ante.HandlerOptions{
		AccountKeeper:   app.AccountKeeper,
		BankKeeper:      app.BankKeeper,
		SignModeHandler: app.txConfig.SignModeHandler(),
		SigGasConsumer:  ante.DefaultSigVerificationGasConsumer,
	}, app.tkeys[TxCountStoreKey], MaxTxPerAccountPerBlock, app.AdminKeeper)
	if err != nil {
		panic(err)
	}
	app.SetAnteHandler(h)
}

func (app *App) setPostHandler() {
	h, err := posthandler.NewPostHandler(posthandler.HandlerOptions{})
	if err != nil {
		panic(err)
	}
	app.SetPostHandler(h)
}

// setABCIHandlers registers the ABCI++ handlers from app/protocol (design §5). User
// txs are selected and checked by the SDK default proposal handler.
func (app *App) setABCIHandlers() {
	veh := protocol.NewVoteExtHandler()
	app.voteExt = veh
	app.SetExtendVoteHandler(func(ctx sdk.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
		start := time.Now()
		res, err := veh.ExtendVote(ctx, req)
		if m := app.metrics; m != nil && res != nil {
			m.ExtendVoteMs.Observe(msSince(start))
			m.VEBytes.Observe(float64(len(res.VoteExtension)))
			m.Full.observeVE(res.VoteExtension, req.Height)
		}
		return res, err
	})
	app.SetVerifyVoteExtensionHandler(veh.VerifyVoteExtension)

	def := baseapp.NewDefaultProposalHandler(app.Mempool(), app)
	ph := protocol.NewProposalHandler(app.Logger(), app.StakingKeeper, def.PrepareProposalHandler(), def.ProcessProposalHandler())
	app.SetPrepareProposal(wrapPrepare(ph.PrepareProposal))
	app.proposals = ph
	app.SetProcessProposal(func(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
		start := time.Now()
		res, err := ph.ProcessProposal(ctx, req)
		if m := app.metrics; m != nil {
			m.ProcessProposalMs.Observe(msSince(start))
		}
		return res, err
	})

	app.protocol = protocol.NewPreBlocker(app.StakingKeeper, protocol.Keepers{
		Oracle:      app.OracleKeeper,
		Vault:       app.VaultKeeper,
		Liquidation: app.LiquidationKeeper,
		Custody:     app.CustodyKeeper,
	})
	// VE versions follow the upgrade schedule (design §17).
	veh.SetSchedule(app.VESchedule)
	ph.SetSchedule(app.VESchedule)
	app.protocol.SetSchedule(app.VESchedule)
}

// Name returns the app name.
func (app *App) Name() string { return app.BaseApp.Name() }

// PreBlocker runs x/upgrade's PreBlock (via the module manager) and then the protocol
// segment (design §5.9).
func (app *App) PreBlocker(ctx sdk.Context, req *abci.RequestFinalizeBlock) (*sdk.ResponsePreBlock, error) {
	res, err := app.ModuleManager.PreBlock(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	app.protocol.PreBlock(ctx, req)
	if m := app.metrics; m != nil {
		m.observePreBlock(ctx, start)
	}
	return res, nil
}

// BeginBlocker runs module BeginBlockers (mint, distribution, slashing, evidence,
// staking).
func (app *App) BeginBlocker(ctx sdk.Context) (sdk.BeginBlock, error) {
	return app.ModuleManager.BeginBlock(ctx)
}

// EndBlocker runs module EndBlockers; x/staking returns the validator updates.
func (app *App) EndBlocker(ctx sdk.Context) (sdk.EndBlock, error) {
	res, err := app.ModuleManager.EndBlock(ctx)
	if err != nil {
		return res, err
	}
	if InvariantsEnabled {
		if err := app.CheckInvariants(ctx); err != nil {
			panic(fmt.Errorf("invariant violated at height %d: %w", ctx.BlockHeight(), err))
		}
	}
	if app.metrics != nil {
		app.observeState(ctx)
	}
	return res, nil
}

// InitChainer initialises state from genesis.
func (app *App) InitChainer(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	var genesisState GenesisState
	if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
		return nil, err
	}
	if err := app.UpgradeKeeper.SetModuleVersionMap(ctx, app.ModuleManager.GetVersionMap()); err != nil {
		return nil, err
	}
	return app.ModuleManager.InitGenesis(ctx, app.appCodec, genesisState)
}

// LoadHeight loads a particular height.
func (app *App) LoadHeight(height int64) error { return app.LoadVersion(height) }

func (app *App) LegacyAmino() *codec.LegacyAmino            { return app.legacyAmino }
func (app *App) AppCodec() codec.Codec                      { return app.appCodec }
func (app *App) InterfaceRegistry() types.InterfaceRegistry { return app.interfaceRegistry }
func (app *App) TxConfig() client.TxConfig                  { return app.txConfig }

// GetKey returns the KV store key for a module (tests only).
func (app *App) GetKey(name string) *storetypes.KVStoreKey { return app.keys[name] }

// DefaultGenesis returns the default genesis of every module, with the Keystone
// staking and mint params (PatchDefaults).
func (app *App) DefaultGenesis() map[string]json.RawMessage {
	return PatchDefaults(app.appCodec, app.BasicModuleManager.DefaultGenesis(app.appCodec))
}

// AutoCliOpts returns the autocli options for the root command.
func (app *App) AutoCliOpts() autocli.AppOptions {
	modules := make(map[string]appmodule.AppModule)
	for name, m := range app.ModuleManager.Modules {
		if am, ok := m.(appmodule.AppModule); ok {
			modules[name] = am
		}
	}
	return autocli.AppOptions{
		Modules:               modules,
		ModuleOptions:         runtimeservices.ExtractAutoCLIOptions(app.ModuleManager.Modules),
		AddressCodec:          authcodec.NewBech32Codec(AccountAddressPrefix),
		ValidatorAddressCodec: authcodec.NewBech32Codec(ValidatorAddressPrefix),
		ConsensusAddressCodec: authcodec.NewBech32Codec(ConsensusAddressPrefix),
	}
}

// RegisterAPIRoutes registers REST routes.
func (app *App) RegisterAPIRoutes(apiSvr *api.Server, apiConfig config.APIConfig) {
	clientCtx := apiSvr.ClientCtx
	authtx.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	cmtservice.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	nodeservice.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	app.BasicModuleManager.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
}

// RegisterTxService registers the tx gRPC service.
func (app *App) RegisterTxService(clientCtx client.Context) {
	authtx.RegisterTxService(app.GRPCQueryRouter(), clientCtx, app.Simulate, app.interfaceRegistry)
}

// RegisterTendermintService registers the CometBFT gRPC service.
func (app *App) RegisterTendermintService(clientCtx client.Context) {
	cmtApp := server.NewCometABCIWrapper(app)
	cmtservice.RegisterTendermintService(clientCtx, app.GRPCQueryRouter(), app.interfaceRegistry, cmtApp.Query)
}

// RegisterNodeService registers the node gRPC service.
func (app *App) RegisterNodeService(clientCtx client.Context, cfg config.Config) {
	nodeservice.RegisterNodeService(clientCtx, app.GRPCQueryRouter(), cfg, func() int64 {
		return app.CommitMultiStore().EarliestVersion()
	})
}

// AdminExecTypes are the SDK messages x/admin MsgExec may run (design §4.2): the
// MsgUpdateParams of the proof-of-stake modules, whose authority is the x/admin module
// account.
func AdminExecTypes() []string {
	return []string{
		sdk.MsgTypeURL(&stakingtypes.MsgUpdateParams{}),
		sdk.MsgTypeURL(&slashingtypes.MsgUpdateParams{}),
		sdk.MsgTypeURL(&distrtypes.MsgUpdateParams{}),
		sdk.MsgTypeURL(&minttypes.MsgUpdateParams{}),
	}
}

// GetMaccPerms returns a copy of the module account permissions.
func GetMaccPerms() map[string][]string { return maps.Clone(maccPerms) }

// BlockedAddresses returns the module account addresses that cannot receive funds.
func BlockedAddresses() map[string]bool {
	out := make(map[string]bool)
	for acc := range GetMaccPerms() {
		out[authtypes.NewModuleAddress(acc).String()] = true
	}
	return out
}

// SetPriceSource attaches the price sidecar to ExtendVote. Call before the node
// starts. If src has a Stop method, Close calls it.
func (app *App) SetPriceSource(src protocol.PriceSource) {
	app.price = src
	app.voteExt.SetPriceSource(src)
}

// SetBitcoinSource attaches the Bitcoin watcher to ExtendVote. Call before the node
// starts.
func (app *App) SetBitcoinSource(src protocol.BitcoinSource) {
	app.bitcoin = src
	app.voteExt.SetBitcoinSource(src)
}

// SetFrostSource attaches the FROST signer to ExtendVote; signer errors are logged.
// Call before the node starts.
func (app *App) SetFrostSource(src protocol.FrostSource) {
	app.voteExt.SetFrostSource(src, func(err error) { app.Logger().Error("frost signer: no FROST payload this vote", "err", err) })
}

// Close stops the sidecars, then closes BaseApp.
func (app *App) Close() error {
	if s, ok := app.price.(interface{ Stop() }); ok {
		s.Stop()
	}
	err := app.BaseApp.Close()
	for _, f := range app.closers {
		err = errors.Join(err, f())
	}
	return err
}

// OnClose registers f to run after BaseApp closes (sidecar resources such as the
// signer's nonce store).
func (app *App) OnClose(f func() error) { app.closers = append(app.closers, f) }

// KVStoreKeyNames returns the names of every KV store, sorted (tests: state diffs).
func (app *App) KVStoreKeyNames() []string {
	names := make([]string, 0, len(app.keys))
	for name := range app.keys {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
