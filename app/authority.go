package app

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	consensuskeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	upgradekeeper "github.com/cosmos/cosmos-sdk/x/upgrade/keeper"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
)

// The x/upgrade and x/consensus keepers take a fixed authority string at
// construction, but the Keystone admin lives in x/admin state and can change. Both
// keepers are built with the x/admin module address as authority (no key can sign for
// it), and their Msg services are wrapped: a message is accepted only if its
// authority field is the current x/admin admin, and is then forwarded with the module
// address substituted.

// AdminChecker is the x/admin check used by the wrappers.
type AdminChecker interface {
	CheckAdmin(ctx context.Context, signer string) error
}

type adminUpgradeMsgServer struct {
	inner     upgradetypes.MsgServer
	admin     AdminChecker
	authority string
}

func (s adminUpgradeMsgServer) SoftwareUpgrade(ctx context.Context, msg *upgradetypes.MsgSoftwareUpgrade) (*upgradetypes.MsgSoftwareUpgradeResponse, error) {
	if err := s.admin.CheckAdmin(ctx, msg.Authority); err != nil {
		return nil, err
	}
	fwd := *msg
	fwd.Authority = s.authority
	return s.inner.SoftwareUpgrade(ctx, &fwd)
}

func (s adminUpgradeMsgServer) CancelUpgrade(ctx context.Context, msg *upgradetypes.MsgCancelUpgrade) (*upgradetypes.MsgCancelUpgradeResponse, error) {
	if err := s.admin.CheckAdmin(ctx, msg.Authority); err != nil {
		return nil, err
	}
	fwd := *msg
	fwd.Authority = s.authority
	return s.inner.CancelUpgrade(ctx, &fwd)
}

// adminUpgradeModule is x/upgrade with its Msg service gated by the x/admin admin.
type adminUpgradeModule struct {
	upgrade.AppModule
	keeper    *upgradekeeper.Keeper
	admin     AdminChecker
	authority string
}

// RegisterServices mirrors upgrade.AppModule.RegisterServices (SDK v0.55.0) with the
// wrapped Msg server.
func (m adminUpgradeModule) RegisterServices(cfg module.Configurator) {
	upgradetypes.RegisterMsgServer(cfg.MsgServer(), adminUpgradeMsgServer{
		inner: upgradekeeper.NewMsgServerImpl(m.keeper), admin: m.admin, authority: m.authority,
	})
	upgradetypes.RegisterQueryServer(cfg.QueryServer(), m.keeper)
	mig := upgradekeeper.NewMigrator(m.keeper)
	if err := cfg.RegisterMigration(upgradetypes.ModuleName, 1, mig.Migrate1to2); err != nil {
		panic(fmt.Sprintf("failed to migrate x/%s from version 1 to 2: %v", upgradetypes.ModuleName, err))
	}
}

type adminConsensusMsgServer struct {
	keeper    consensuskeeper.Keeper
	admin     AdminChecker
	authority string
}

func (s adminConsensusMsgServer) UpdateParams(ctx context.Context, msg *consensustypes.MsgUpdateParams) (*consensustypes.MsgUpdateParamsResponse, error) {
	if err := s.admin.CheckAdmin(ctx, msg.Authority); err != nil {
		return nil, err
	}
	fwd := *msg
	fwd.Authority = s.authority
	return s.keeper.UpdateParams(ctx, &fwd)
}

// adminConsensusModule is x/consensus with its Msg service gated by the x/admin admin.
type adminConsensusModule struct {
	consensus.AppModule
	keeper    consensuskeeper.Keeper
	admin     AdminChecker
	authority string
}

// RegisterServices mirrors consensus.AppModule.RegisterServices (SDK v0.55.0) with the
// wrapped Msg server.
func (m adminConsensusModule) RegisterServices(registrar grpc.ServiceRegistrar) error {
	consensustypes.RegisterMsgServer(registrar, adminConsensusMsgServer{keeper: m.keeper, admin: m.admin, authority: m.authority})
	consensustypes.RegisterQueryServer(registrar, m.keeper)
	return nil
}
