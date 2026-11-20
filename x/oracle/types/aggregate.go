package types

import (
	"bytes"
	"sort"

	sdkmath "cosmossdk.io/math"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
)

// Report is one verified vote's oracle input. Payload is nil when the VE carried no
// oracle payload.
type Report struct {
	ConsAddr []byte
	Power    int64
	Payload  *vetypes.OraclePayload
}

// PriceAggregate is the oracle part of the aggregated VEs.
type PriceAggregate struct {
	// Updated is true when reporters exceeded 2/3 of total power.
	Updated bool
	// Median is the power-weighted (lower) median of usable reports, micro-USD.
	Median uint64
	// Spot is Median clamped to ±MaxStepBps of the previous spot (no clamp at bootstrap).
	Spot sdkmath.Int
	// ReportedPower and TotalPower are the quorum inputs.
	ReportedPower int64
	TotalPower    int64
	// ReporterAddrs are the consensus addresses of usable reports, in price order.
	ReporterAddrs [][]byte
	// Reports are every verified vote, for miss accounting.
	Reports []Report
}

// Usable reports whether a payload can enter the median: present, non-zero price and
// at least MinSources quotes.
func Usable(p *vetypes.OraclePayload, params Params) bool {
	return p != nil && p.PriceMicroUsd > 0 && p.NumSources >= params.MinSources
}

// AggregatePrice is design §6.3 exactly. totalPower is the power of every entry of
// the injected commit, including absent validators.
func AggregatePrice(reports []Report, totalPower int64, prevSpot sdkmath.Int, p Params) PriceAggregate {
	type obs struct {
		price uint64
		power int64
		addr  []byte
	}
	var xs []obs
	var reported int64
	for _, r := range reports {
		if !Usable(r.Payload, p) {
			continue
		}
		xs = append(xs, obs{r.Payload.PriceMicroUsd, r.Power, r.ConsAddr})
		reported += r.Power
	}
	sort.SliceStable(xs, func(i, j int) bool {
		if xs[i].price != xs[j].price {
			return xs[i].price < xs[j].price
		}
		return bytes.Compare(xs[i].addr, xs[j].addr) < 0
	})
	agg := PriceAggregate{ReportedPower: reported, TotalPower: totalPower, Reports: reports, Spot: sdkmath.ZeroInt()}
	for _, o := range xs {
		agg.ReporterAddrs = append(agg.ReporterAddrs, o.addr)
	}
	if reported*3 <= totalPower*2 {
		return agg // Updated = false
	}

	var cum int64
	for _, o := range xs {
		cum += o.power
		if cum*2 >= reported {
			agg.Median = o.price
			break
		}
	}
	agg.Spot = sdkmath.NewIntFromUint64(agg.Median)
	if !prevSpot.IsNil() && !prevSpot.IsZero() { // no clamp at bootstrap
		lo := prevSpot.MulRaw(int64(10_000 - p.MaxStepBps)).QuoRaw(10_000)
		hi := prevSpot.MulRaw(int64(10_000 + p.MaxStepBps)).QuoRaw(10_000)
		agg.Spot = clampInt(agg.Spot, lo, hi)
	}
	agg.Updated = true
	return agg
}

func clampInt(v, lo, hi sdkmath.Int) sdkmath.Int {
	if v.LT(lo) {
		return lo
	}
	if v.GT(hi) {
		return hi
	}
	return v
}

// IsOutlier reports |price − median| × 10,000 > median × OutlierBps (design §6.6).
func IsOutlier(price, median uint64, p Params) bool {
	pr, md := sdkmath.NewIntFromUint64(price), sdkmath.NewIntFromUint64(median)
	diff := pr.Sub(md).Abs()
	return diff.MulRaw(10_000).GT(md.MulRaw(int64(p.OutlierBps)))
}
