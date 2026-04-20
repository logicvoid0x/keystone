package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/OWNER/keystone/testutil/apptest"
	"github.com/OWNER/keystone/x/vault"
	vaulttypes "github.com/OWNER/keystone/x/vault/types"
)

// MsgDevCreditCollateral is routed only in binaries built with -tags devcredit.
func TestDevCreditRoutedOnlyWithTag(t *testing.T) {
	c := apptest.New(t, 1)
	h := c.App.MsgServiceRouter().Handler(&vaulttypes.MsgDevCreditCollateral{})
	if vault.DevCreditEnabled {
		require.NotNil(t, h)
	} else {
		require.Nil(t, h)
	}
	require.NotNil(t, c.App.MsgServiceRouter().Handler(sdk.Msg(&vaulttypes.MsgMint{})))
}
