package protocol

import (
	"context"

	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
	oracletypes "github.com/logicvoid0x/keystone/x/oracle/types"
)

// OracleView is what aggregation reads from x/oracle: the previous spot (for the
// clamp) and the params.
type OracleView interface {
	GetPriceState(ctx context.Context) (oracletypes.PriceState, error)
	GetParams(ctx context.Context) (oracletypes.Params, error)
}

// Aggregated is the result of aggregating verified VEs.
type Aggregated struct {
	Oracle oracletypes.PriceAggregate
	// Custody holds each verified vote's Bitcoin and FROST payloads, in commit order,
	// for x/custody (design §9.7, §9.11).
	Custody []custodytypes.Vote
	// Voters is the number of verified votes.
	Voters int
	// VerifiedPower and TotalPower are from VerifyVotes.
	VerifiedPower int64
	TotalPower    int64
}

// Aggregate combines verified votes (design §5.9 step 2). The oracle part is
// oracletypes.AggregatePrice (§6.3) against the committed spot; the Bitcoin and FROST
// parts are passed through to custody.BuildOps (tallies, nonce pools, signing
// sessions). total is the power of every entry of the injected commit.
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
	out.Custody = make([]custodytypes.Vote, len(votes))
	for i, v := range votes {
		reports[i] = oracletypes.Report{ConsAddr: v.ConsAddr, Power: v.Power, Payload: v.VE.Oracle}
		out.Custody[i] = custodytypes.Vote{ConsAddr: v.ConsAddr, Bitcoin: v.VE.Bitcoin, Frost: v.VE.Frost}
	}
	out.Oracle = oracletypes.AggregatePrice(reports, total, ps.SpotMicroUsd, p)
	return out, nil
}
