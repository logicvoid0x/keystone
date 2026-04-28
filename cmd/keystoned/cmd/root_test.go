package cmd_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/cmd/keystoned/cmd"
	"github.com/OWNER/keystone/x/vault"
)

// Spec M1-T9: autocli commands for every message and query in M1.
func TestM1Commands(t *testing.T) {
	root := cmd.NewRootCmd()
	paths := []string{
		"tx vault open-vault", "tx vault mint", "tx vault repay", "tx vault withdraw-collateral",
		"tx vault close-vault", "tx vault deposit-claim", "tx vault update-params",
		"tx liquidation deposit-pool", "tx liquidation withdraw-pool", "tx liquidation take-collateral",
		"tx liquidation update-params", "tx oracle update-params", "tx stablecoin update-params",
		"tx custody update-params",
		"q vault vault", "q vault vaults-by-owner", "q vault rate-state", "q vault liquidation-queue", "q vault params",
		"q liquidation pool", "q liquidation pool-deposit", "q liquidation auction", "q liquidation auctions",
		"q oracle price", "q oracle twap", "q oracle miss-counter",
		"q stablecoin surplus", "q stablecoin bad-debt", "q custody claimable",
		"q poa validators",
	}
	for _, p := range paths {
		c, rest, err := root.Find(strings.Fields(p))
		require.NoError(t, err, p)
		require.Empty(t, rest, p)
		require.Equal(t, strings.Fields(p)[len(strings.Fields(p))-1], c.Name(), p)
	}
	c, _, err := root.Find([]string{"tx", "vault", "dev", "credit-collateral"})
	if vault.DevCreditEnabled {
		require.NoError(t, err)
		require.Equal(t, "credit-collateral", c.Name())
	} else {
		require.True(t, err != nil || c.Name() != "credit-collateral", "dev message only in devcredit builds")
	}
}
