package btcwatch

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cast"

	"cosmossdk.io/log/v2"
)

// app.toml keys under [keystone.bitcoin].
const (
	FlagEnabled      = "keystone.bitcoin.enabled"
	FlagNetwork      = "keystone.bitcoin.network"
	FlagRPCURL       = "keystone.bitcoin.rpc-url"
	FlagRPCUser      = "keystone.bitcoin.rpc-user"
	FlagRPCPassword  = "keystone.bitcoin.rpc-password"
	FlagZMQBlock     = "keystone.bitcoin.zmq-block"
	FlagPollInterval = "keystone.bitcoin.poll-interval"
	FlagFixedFeeRate = "keystone.bitcoin.fixed-feerate"
	FlagScanWindow   = "keystone.bitcoin.scan-window"
)

// FileConfig is the [keystone.bitcoin] section of app.toml.
type FileConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Network      string `mapstructure:"network"`
	RPCURL       string `mapstructure:"rpc-url"`
	RPCUser      string `mapstructure:"rpc-user"`
	RPCPassword  string `mapstructure:"rpc-password"`
	ZMQBlock     string `mapstructure:"zmq-block"`
	PollInterval string `mapstructure:"poll-interval"`
	FixedFeeRate uint64 `mapstructure:"fixed-feerate"`
	ScanWindow   int64  `mapstructure:"scan-window"`
}

// DefaultFileConfig is disabled (no bitcoind).
func DefaultFileConfig() FileConfig {
	return FileConfig{
		Network: "signet", RPCURL: "http://127.0.0.1:38332",
		PollInterval: DefaultPollInterval.String(), ScanWindow: DefaultScanWindow,
	}
}

// RegtestFileConfig points at a regtest bitcoind (localnet).
func RegtestFileConfig(rpcURL, user, pass, zmq string) FileConfig {
	c := DefaultFileConfig()
	c.Enabled, c.Network, c.RPCURL, c.RPCUser, c.RPCPassword, c.ZMQBlock = true, "regtest", rpcURL, user, pass, zmq
	c.FixedFeeRate = 2
	return c
}

// ConfigTemplate is appended to the SDK app.toml template.
const ConfigTemplate = `
###############################################################################
###                         Keystone Bitcoin watcher                        ###
###############################################################################

[keystone.bitcoin]

# Follow a bitcoind and attest deposits in vote extensions.
enabled = {{ .Keystone.Bitcoin.Enabled }}
# Must equal the chain's custody bitcoin_network param: "regtest" or "signet".
network = "{{ .Keystone.Bitcoin.Network }}"
rpc-url = "{{ .Keystone.Bitcoin.RPCURL }}"
rpc-user = "{{ .Keystone.Bitcoin.RPCUser }}"
rpc-password = "{{ .Keystone.Bitcoin.RPCPassword }}"
# bitcoind -zmqpubhashblock endpoint; empty = poll only.
zmq-block = "{{ .Keystone.Bitcoin.ZMQBlock }}"
poll-interval = "{{ .Keystone.Bitcoin.PollInterval }}"
# Fixed fee rate in sat/vB (regtest has no estimates); 0 = estimatesmartfee.
fixed-feerate = {{ .Keystone.Bitcoin.FixedFeeRate }}
# Number of recent blocks scanned for deposits and reorgs.
scan-window = {{ .Keystone.Bitcoin.ScanWindow }}
`

// AppOptions is the subset of servertypes.AppOptions used here.
type AppOptions interface{ Get(string) any }

// StartFromAppOptions starts a watcher from app.toml, if enabled. It returns nil when
// the watcher is disabled.
func StartFromAppOptions(ctx context.Context, opts AppOptions, view ChainView, logger log.Logger) (*Watcher, error) {
	if !cast.ToBool(opts.Get(FlagEnabled)) {
		return nil, nil
	}
	network := cast.ToString(opts.Get(FlagNetwork))
	if network != "regtest" && network != "signet" {
		return nil, fmt.Errorf("%s must be regtest or signet, got %q", FlagNetwork, network)
	}
	poll := DefaultPollInterval
	if s := cast.ToString(opts.Get(FlagPollInterval)); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", FlagPollInterval, err)
		}
		poll = d
	}
	rpc := NewRPC(cast.ToString(opts.Get(FlagRPCURL)), cast.ToString(opts.Get(FlagRPCUser)), cast.ToString(opts.Get(FlagRPCPassword)))
	rpc.FixedFeeRate = cast.ToUint64(opts.Get(FlagFixedFeeRate))
	w := New(chainChecked{rpc, network}, view, Config{Network: network, ScanWindow: cast.ToInt64(opts.Get(FlagScanWindow)), PollInterval: poll}, logger)
	var trigger <-chan struct{}
	if zmq := cast.ToString(opts.Get(FlagZMQBlock)); zmq != "" {
		trigger = ZMQTrigger(ctx, zmq, logger)
	}
	go w.Run(ctx, trigger)
	return w, nil
}

// chainChecked refuses to report a tip until bitcoind's chain matches network.
type chainChecked struct {
	*RPC
	network string
}

func (c chainChecked) TipHeight(ctx context.Context) (int64, error) {
	chain, err := c.Chain(ctx)
	if err != nil {
		return 0, err
	}
	if chain != c.network {
		return 0, fmt.Errorf("bitcoind is on %q, watcher expects %q", chain, c.network)
	}
	return c.RPC.TipHeight(ctx)
}
