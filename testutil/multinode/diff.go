package multinode

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/OWNER/keystone/app"
)

// maxDiffs caps the keys reported per store.
const maxDiffs = 10

// DiffStores compares every KV store of two apps at their committed state and
// describes the first differing keys of each store ("" if identical).
func DiffStores(a, b *app.App) string {
	var out strings.Builder
	for _, name := range a.KVStoreKeyNames() {
		sa := a.CommitMultiStore().GetKVStore(a.GetKey(name))
		sb := b.CommitMultiStore().GetKVStore(b.GetKey(name))
		ia, ib := sa.Iterator(nil, nil), sb.Iterator(nil, nil)
		n := 0
		for (ia.Valid() || ib.Valid()) && n < maxDiffs {
			switch {
			case !ib.Valid() || (ia.Valid() && bytes.Compare(ia.Key(), ib.Key()) < 0):
				fmt.Fprintf(&out, "store %s: key %X only in node A (value %X)\n", name, ia.Key(), ia.Value())
				ia.Next()
				n++
			case !ia.Valid() || bytes.Compare(ib.Key(), ia.Key()) < 0:
				fmt.Fprintf(&out, "store %s: key %X only in node B (value %X)\n", name, ib.Key(), ib.Value())
				ib.Next()
				n++
			default:
				if !bytes.Equal(ia.Value(), ib.Value()) {
					fmt.Fprintf(&out, "store %s: key %X differs: A %X, B %X\n", name, ia.Key(), ia.Value(), ib.Value())
					n++
				}
				ia.Next()
				ib.Next()
			}
		}
		ia.Close()
		ib.Close()
	}
	return out.String()
}
