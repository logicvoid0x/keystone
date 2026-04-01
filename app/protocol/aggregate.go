package protocol

// Aggregated is the result of aggregating verified VEs. M1 adds the oracle aggregate,
// M3 the Bitcoin tallies and M4a the FROST shares.
type Aggregated struct {
	// Voters is the number of verified votes.
	Voters int
	// VerifiedPower and TotalPower are from VerifyVotes.
	VerifiedPower int64
	TotalPower    int64
}

// Aggregate combines verified votes. M0 stub: counts only.
func Aggregate(votes []VerifiedVote, verified, total int64) Aggregated {
	return Aggregated{Voters: len(votes), VerifiedPower: verified, TotalPower: total}
}
