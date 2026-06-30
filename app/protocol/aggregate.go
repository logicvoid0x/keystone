package protocol

import (
	"context"

	custodytypes "github.com/OWNER/keystone/x/custody/types"
	oracletypes "github.com/OWNER/keystone/x/oracle/types"
)

// OracleView is what aggregation reads from x/oracle: the previous spot (for the
// clamp) and the params.
type OracleView interface {
	GetPriceState(ctx context.Context) (oracletypes.PriceState, error)
	GetParams(ctx context.Context) (oracletypes.Params, error)
}

// Aggregated is the result of aggregating verified VEs. M4a adds the FROST shares.
type Aggregated struct {
	Oracle oracletypes.PriceAggregate
	// Bitcoin holds each verified vote's Bitcoin payload, in commit order, for the
	// x/custody tallies (design §9.7).
	Bitcoin []custodytypes.BitcoinVote
	// Voters is the number of verified votes.
	Voters int
	// VerifiedPower and TotalPower are from VerifyVotes.
	VerifiedPower int64
	TotalPower    int64
}

// Aggregate combines verified votes (design §5.9 step 2). The oracle part is
// oracletypes.AggregatePrice (§6.3) against the committed spot; the Bitcoin part is
// passed through to custody.BuildOps, which tallies it across heights. total is the power of
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
	out.Bitcoin = make([]custodytypes.BitcoinVote, len(votes))
	for i, v := range votes {
		reports[i] = oracletypes.Report{ConsAddr: v.ConsAddr, Power: v.Power, Payload: v.VE.Oracle}
		out.Bitcoin[i] = custodytypes.BitcoinVote{ConsAddr: v.ConsAddr, Payload: v.VE.Bitcoin}
	}
	out.Oracle = oracletypes.AggregatePrice(reports, total, ps.SpotMicroUsd, p)
	return out, nil
}
