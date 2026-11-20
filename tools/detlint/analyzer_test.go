package detlint_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/logicvoid0x/keystone/tools/detlint"
)

func TestAnalyzer(t *testing.T) {
	dir := analysistest.TestData()
	analysistest.Run(t, dir, detlint.Analyzer,
		"keystone/x/bad",
		"keystone/x/good",
		"keystone/x/noreason",
		"keystone/x/secretimport",
		"keystone/sidecar/price",
		"keystone/crypto/frost/secret",
		"keystone/app/protocol",
	)
}

func TestInScope(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"github.com/logicvoid0x/keystone/x/poa/keeper", true},
		{"github.com/logicvoid0x/keystone/x/poa", true},
		{"github.com/logicvoid0x/keystone/app/protocol", true},
		{"github.com/logicvoid0x/keystone/crypto/frost", true},
		{"github.com/logicvoid0x/keystone/crypto/frost/secret", false},
		{"github.com/logicvoid0x/keystone/crypto/frost/secret/store", false},
		{"github.com/logicvoid0x/keystone/sidecar/price", false},
		{"github.com/logicvoid0x/keystone/app", false},
		{"github.com/logicvoid0x/keystone/cmd/keystoned", false},
		{"github.com/logicvoid0x/keystone/app/protocolx", false},
		{"github.com/logicvoid0x/keystone/x/poa_test", true},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, detlint.InScope(tc.path), tc.path)
	}
}
