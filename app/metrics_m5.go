package app

import (
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/logicvoid0x/keystone/app/protocol"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
	vaulttypes "github.com/logicvoid0x/keystone/x/vault/types"
)

// FullMetrics are the rest of the design §16 metrics (spec M5-T6), read like the M2
// ones: gauges from read-only keeper queries at EndBlock, local values from this
// node's own VE at ExtendVote.
type FullMetrics struct {
	OracleLocalPrice      prometheus.Gauge
	OracleLocalSources    prometheus.Gauge
	OracleMisses          *prometheus.GaugeVec
	Surplus               prometheus.Gauge
	Sin                   prometheus.Gauge
	LiquidationQueueDepth prometheus.Gauge
	CustodiedSat          prometheus.Gauge
	CreditedSat           prometheus.Gauge
	TreasuryCapSat        prometheus.Gauge
	FeeReserveSat         prometheus.Gauge
	DeficitSat            prometheus.Gauge
	UtxoCount             *prometheus.GaugeVec
	WithdrawalQueueDepth  prometheus.Gauge
	BatchAgeBlocks        prometheus.Gauge
	SessionAttempts       prometheus.Gauge
	SessionLatencyBlocks  prometheus.Gauge
	SessionsStalled       prometheus.Gauge
	NoncePoolLevel        *prometheus.GaugeVec
	BtcAnchorHeight       prometheus.Gauge
	BitcoindTipAgeSeconds prometheus.Gauge
	NonceAlarms           prometheus.Counter
}

func gauge(name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: name, Help: help})
}

func gaugeVec(name, help, label string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: name, Help: help}, []string{label})
}

func newFullMetrics(reg prometheus.Registerer) (*FullMetrics, error) {
	m := &FullMetrics{
		OracleLocalPrice:      gauge("oracle_local_price", "This node's local median price in its last VE, micro-USD (0 if none)."),
		OracleLocalSources:    gauge("oracle_local_sources", "Sources in this node's last local median."),
		OracleMisses:          gaugeVec("oracle_misses", "Oracle misses in the miss window, by validator consensus address.", "validator"),
		Surplus:               gauge("surplus", "Surplus buffer, ukusd."),
		Sin:                   gauge("sin", "Unbacked debt (sin), ukusd."),
		LiquidationQueueDepth: gauge("liquidation_queue_depth", "Vaults liquidatable at the current liquidation price."),
		CustodiedSat:          gauge("custodied_sat", "Satoshis in Keystone UTXOs."),
		CreditedSat:           gauge("credited_sat", "Satoshis credited as vault collateral."),
		TreasuryCapSat:        gauge("treasury_cap_sat", "Treasury cap on credited satoshis."),
		FeeReserveSat:         gauge("fee_reserve_sat", "Custody fee reserve, sat."),
		DeficitSat:            gauge("deficit_sat", "Custody deficit, sat."),
		UtxoCount:             gaugeVec("utxo_count", "Keystone UTXOs by status.", "status"),
		WithdrawalQueueDepth:  gauge("withdrawal_queue_depth", "Queued withdrawal requests."),
		BatchAgeBlocks:        gauge("batch_age_blocks", "Age in blocks of the oldest unconfirmed batch (0 if none)."),
		SessionAttempts:       gauge("session_attempts", "Highest attempt number among active signing sessions."),
		SessionLatencyBlocks:  gauge("session_latency_blocks", "Age in blocks of the oldest active signing session."),
		SessionsStalled:       gauge("sessions_stalled", "STALLED signing sessions."),
		NoncePoolLevel:        gaugeVec("nonce_pool_level", "Unconsumed nonce commitments of each share of the active epoch.", "share"),
		BtcAnchorHeight:       gauge("btc_anchor_height", "Accepted Bitcoin anchor height."),
		BitcoindTipAgeSeconds: gauge("bitcoind_tip_age_seconds", "Age of this node's bitcoind tip (watcher), seconds."),
		NonceAlarms: prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "nonce_alarms_total",
			Help: "Times this node's signer refused to bind a nonce already bound to another session."}),
	}
	for _, c := range []prometheus.Collector{
		m.OracleLocalPrice, m.OracleLocalSources, m.OracleMisses, m.Surplus, m.Sin, m.LiquidationQueueDepth,
		m.CustodiedSat, m.CreditedSat, m.TreasuryCapSat, m.FeeReserveSat, m.DeficitSat, m.UtxoCount, m.WithdrawalQueueDepth,
		m.BatchAgeBlocks, m.SessionAttempts, m.SessionLatencyBlocks, m.SessionsStalled, m.NoncePoolLevel,
		m.BtcAnchorHeight, m.BitcoindTipAgeSeconds, m.NonceAlarms,
	} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register metric: %w", err)
		}
	}
	return m, nil
}

// observeVE records this node's own VE (local price and sources).
func (m *FullMetrics) observeVE(bz []byte, height int64) {
	ve, err := protocol.DecodeVE(bz, height)
	if err != nil || ve.Oracle == nil {
		m.OracleLocalPrice.Set(0)
		m.OracleLocalSources.Set(0)
		return
	}
	m.OracleLocalPrice.Set(float64(ve.Oracle.PriceMicroUsd))
	m.OracleLocalSources.Set(float64(ve.Oracle.NumSources))
}

// observeFullState sets the M5 gauges from read-only keeper queries.
func (app *App) observeFullState(ctx sdk.Context) {
	m := app.metrics.Full
	h := ctx.BlockHeight()
	m.Surplus.Set(intFloat(app.StablecoinKeeper.SurplusBalance(ctx)))
	if s, err := app.StablecoinKeeper.GetSin(ctx); err == nil {
		m.Sin.Set(intFloat(s))
	}
	if price, ok, err := app.OracleKeeper.LiquidationPrice(ctx); err == nil && ok {
		n := 0
		_ = app.VaultKeeper.IterateLiquidatable(ctx, price, func(_ vaulttypes.Vault) bool { n++; return n >= 10_000 })
		m.LiquidationQueueDepth.Set(float64(n))
	}
	_ = app.OracleKeeper.MissCount.Walk(ctx, nil, func(cons []byte, n uint32) (bool, error) {
		m.OracleMisses.WithLabelValues(sdk.ConsAddress(cons).String()).Set(float64(n))
		return false, nil
	})

	k := app.CustodyKeeper
	if v, err := k.UtxoSum(ctx); err == nil {
		m.CustodiedSat.Set(intFloat(v))
	}
	if v, err := k.GetCredited(ctx); err == nil {
		m.CreditedSat.Set(intFloat(v))
	}
	if p, err := k.GetParams(ctx); err == nil {
		m.TreasuryCapSat.Set(intFloat(p.TreasuryCapSat))
	}
	if v, err := k.GetFeeReserve(ctx); err == nil {
		m.FeeReserveSat.Set(intFloat(v))
	}
	if v, err := k.GetDeficit(ctx); err == nil {
		m.DeficitSat.Set(intFloat(v))
	}
	counts := map[custodytypes.UtxoStatus]int{}
	_ = k.Utxos.Walk(ctx, nil, func(_ collections.Pair[[]byte, uint32], u custodytypes.Utxo) (bool, error) {
		counts[u.Status]++
		return false, nil
	})
	for _, s := range []custodytypes.UtxoStatus{custodytypes.UTXO_STATUS_AVAILABLE, custodytypes.UTXO_STATUS_LOCKED} {
		m.UtxoCount.WithLabelValues(s.String()).Set(float64(counts[s]))
	}
	q := 0
	_ = k.Queue.Walk(ctx, nil, func(uint64) (bool, error) { q++; return false, nil })
	m.WithdrawalQueueDepth.Set(float64(q))
	var age int64
	_ = k.InFlight.Walk(ctx, nil, func(_ uint64, id uint64) (bool, error) {
		if b, err := k.Batches.Get(ctx, id); err == nil {
			age = max(age, h-b.OpenedHeight)
		}
		return false, nil
	})
	m.BatchAgeBlocks.Set(float64(age))
	var attempts, latency int64
	stalled := 0
	if ss, err := k.GetActiveSessions(ctx); err == nil {
		for _, s := range ss {
			attempts = max(attempts, int64(s.Attempt))
			latency = max(latency, h-s.OpenedHeight)
			if s.State == custodytypes.SESSION_STATE_STALLED {
				stalled++
			}
		}
	}
	m.SessionAttempts.Set(float64(attempts))
	m.SessionLatencyBlocks.Set(float64(latency))
	m.SessionsStalled.Set(float64(stalled))
	if e, ok, err := k.GetActiveEpoch(ctx); err == nil && ok {
		for i := uint32(1); i <= e.N; i++ {
			if _, pool, err := k.NoncePool(ctx, e.Number, i); err == nil {
				m.NoncePoolLevel.WithLabelValues(strconv.FormatUint(uint64(i), 10)).Set(float64(len(pool)))
			}
		}
	}
	if a, err := k.Anchor.Get(ctx); err == nil {
		m.BtcAnchorHeight.Set(float64(a.Height))
	}
	if t, ok := app.bitcoin.(interface{ TipAge() (float64, bool) }); ok {
		if age, ok := t.TipAge(); ok {
			m.BitcoindTipAgeSeconds.Set(age)
		}
	}
}
