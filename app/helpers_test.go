package app_test

import (
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"

	"cosmossdk.io/log/v2"
)

type appOpts map[string]any

func (o appOpts) Get(k string) any { return o[k] }

func nopLogger() log.Logger { return log.NewNopLogger() }

func memDB() dbm.DB { return dbm.NewMemDB() }

type abciEvent = abci.Event
