package secretimport

import (
	_ "keystone/crypto/frost/secret" // want `import of "keystone/crypto/frost/secret" \(FROST secret operations\) is not allowed in state-machine code`
)
