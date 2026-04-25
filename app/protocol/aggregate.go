package protocol

import (
	"context"

	oracletypes "github.com/OWNER/keystone/x/oracle/types"
)

// OracleView is what aggregation reads from x/oracle: the previous spot (for the
// clamp) and the params.
type OracleView interface {
	GetPriceState(ctx context.Context) (oracletypes.PriceState, error)
	GetParams(ctx context.Context) (oracletypes.Params, error)
}

// Aggregated is the result of aggregating verified VEs. M3 adds the Bitcoin tallies
// and M4a the FROST shares; both are empty in M1.
type Aggregated struct {
	Oracle oracletypes.PriceAggregate
	// Voters is the number of verified votes.
	Voters int
	// VerifiedPower and TotalPower are from VerifyVotes.
	VerifiedPower int64
	TotalPower    int64
}

// Aggregate combines verified votes (design §5.9 step 2). The oracle part is
// oracletypes.AggregatePrice (§6.3) against the committed spot. total is the power of
// every entry of the injected commit.
func Aggregate(ctx context.Context, oracle OracleView, votes []VerifiedVote, verified, total int64) (Aggregated, error) {
	out := Aggregated{Voters: len(votes), VerifiedPower: verified, TotalPower: total}
	ps, err := oracle.GetPriceState(ctx)
	if err != nil {
		return out, err
	}
	p, err := oracle.GetParams(ctx)
	if err != nil {
		return out, err
	}
	reports := make([]oracletypes.Report, len(votes))
	for i, v := range votes {
		reports[i] = oracletypes.Report{ConsAddr: v.ConsAddr, Power: v.Power, Payload: v.VE.Oracle}
	}
	out.Oracle = oracletypes.AggregatePrice(reports, total, ps.SpotMicroUsd, p)
	return out, nil
}
