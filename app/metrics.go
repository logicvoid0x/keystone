package app

import (
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	liqtypes "github.com/logicvoid0x/keystone/x/liquidation/types"
	oracletypes "github.com/logicvoid0x/keystone/x/oracle/types"
	vaulttypes "github.com/logicvoid0x/keystone/x/vault/types"
)

// Metrics are the design §16 Prometheus metrics available in M2 (namespace keystone).
// They only observe: timings are taken here in app/ (not app/protocol, which may not
// read the clock) and gauges are read-only keeper queries, so metrics never affect state.
type Metrics struct {
	ExtendVoteMs           prometheus.Histogram
	VEBytes                prometheus.Histogram
	ProcessProposalMs      prometheus.Histogram
	ProcessProposalRejects *prometheus.CounterVec
	PreBlockerMs           prometheus.Histogram
	VEUnverified           *prometheus.CounterVec
	OracleSpot             prometheus.Gauge
	OracleParticipationBps prometheus.Gauge
	OracleStatus           prometheus.Gauge
	VaultCount             prometheus.Gauge
	DebtTotal              prometheus.Gauge
	LiquidationsTotal      *prometheus.CounterVec
	PoolBalance            prometheus.Gauge
	// Full holds the rest of the §16 metrics (M5).
	Full *FullMetrics
}

const metricsNamespace = "keystone"

// NewMetrics creates the metrics and registers them with reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	ms := []float64{0.1, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 1000}
	m := &Metrics{
		ExtendVoteMs: prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "extend_vote_ms",
			Help: "ExtendVote duration in milliseconds.", Buckets: ms}),
		VEBytes: prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "ve_bytes",
			Help: "Size of this node's vote extensions.", Buckets: prometheus.ExponentialBuckets(16, 2, 11)}),
		ProcessProposalMs: prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "process_proposal_ms",
			Help: "ProcessProposal duration in milliseconds.", Buckets: ms}),
		ProcessProposalRejects: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "process_proposal_rejects",
			Help: "Proposals this node rejected, by reason."}, []string{"reason"}),
		PreBlockerMs: prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: metricsNamespace, Name: "preblocker_ms",
			Help: "PreBlocker (protocol segment) duration in milliseconds.", Buckets: ms}),
		VEUnverified: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "ve_unverified",
			Help: "Committed votes whose VE did not verify, by validator consensus address."}, []string{"validator"}),
		OracleSpot: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "oracle_spot",
			Help: "Committed spot price, micro-USD per BTC."}),
		OracleParticipationBps: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "oracle_participation_bps",
			Help: "Reported power of the last price update, bps of total (0 when the block had no update)."}),
		OracleStatus: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "oracle_status",
			Help: "Oracle status: 1 ACTIVE, 2 VOLATILE, 3 STALE."}),
		VaultCount: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "vault_count",
			Help: "Number of vaults."}),
		DebtTotal: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "debt_total",
			Help: "Total vault debt, ukusd."}),
		LiquidationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricsNamespace, Name: "liquidations_total",
			Help: "Vaults liquidated, by mode."}, []string{"mode"}),
		PoolBalance: prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricsNamespace, Name: "pool_balance",
			Help: "Stability pool deposits, ukusd."}),
	}
	for _, c := range []prometheus.Collector{
		m.ExtendVoteMs, m.VEBytes, m.ProcessProposalMs, m.ProcessProposalRejects, m.PreBlockerMs, m.VEUnverified,
		m.OracleSpot, m.OracleParticipationBps, m.OracleStatus, m.VaultCount, m.DebtTotal, m.LiquidationsTotal, m.PoolBalance,
	} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register metric: %w", err)
		}
	}
	full, err := newFullMetrics(reg)
	if err != nil {
		return nil, err
	}
	m.Full = full
	return m, nil
}

// EnableMetrics starts recording into m (nil disables).
func (app *App) EnableMetrics(m *Metrics) {
	app.metrics = m
	if m == nil {
		app.proposals.SetRejectObserver(nil)
		app.protocol.SetUnverifiedObserver(nil)
		return
	}
	app.proposals.SetRejectObserver(func(reason string) { m.ProcessProposalRejects.WithLabelValues(reason).Inc() })
	app.protocol.SetUnverifiedObserver(func(cons []byte) {
		m.VEUnverified.WithLabelValues(sdk.ConsAddress(cons).String()).Inc()
	})
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }

// observePreBlock records the protocol segment's duration and outcome.
func (m *Metrics) observePreBlock(ctx sdk.Context, start time.Time) {
	m.PreBlockerMs.Observe(msSince(start))
	participation := 0.0
	for _, e := range ctx.EventManager().Events() {
		switch e.Type {
		case liqtypes.EventTypeVaultLiquidated:
			for _, a := range e.Attributes {
				if a.Key == liqtypes.AttributeKeyMode {
					m.LiquidationsTotal.WithLabelValues(a.Value).Inc()
				}
			}
		case oracletypes.EventTypePriceUpdated:
			for _, a := range e.Attributes {
				if a.Key == oracletypes.AttributeKeyParticipationBps {
					if v, err := strconv.ParseFloat(a.Value, 64); err == nil {
						participation = v
					}
				}
			}
		}
	}
	m.OracleParticipationBps.Set(participation)
}

// observeState sets the state gauges from read-only keeper queries.
func (app *App) observeState(ctx sdk.Context) {
	m := app.metrics
	if ps, err := app.OracleKeeper.GetPriceState(ctx); err == nil {
		m.OracleSpot.Set(intFloat(ps.SpotMicroUsd))
		m.OracleStatus.Set(float64(ps.Status))
	}
	if d, err := app.VaultKeeper.TotalDebt(ctx); err == nil {
		m.DebtTotal.Set(intFloat(d))
	}
	if p, err := app.LiquidationKeeper.PoolBalance(ctx); err == nil {
		m.PoolBalance.Set(intFloat(p))
	}
	n := 0
	_ = app.VaultKeeper.Vaults.Walk(ctx, nil, func(uint64, vaulttypes.Vault) (bool, error) { n++; return false, nil })
	m.VaultCount.Set(float64(n))
	app.observeFullState(ctx)
}

func intFloat(v sdkmath.Int) float64 {
	if v.IsNil() {
		return 0
	}
	f, _ := new(big.Float).SetInt(v.BigInt()).Float64()
	return f
}

// Metrics returns the enabled metrics (nil if disabled).
func (app *App) Metrics() *Metrics { return app.metrics }
