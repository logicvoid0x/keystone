package multinode_test

import "github.com/logicvoid0x/keystone/testutil/multinode"

// steps runs n honest heights.
func steps(net *multinode.Net, n int) {
	for i := 0; i < n; i++ {
		net.Step(multinode.StepOpts{})
	}
}
