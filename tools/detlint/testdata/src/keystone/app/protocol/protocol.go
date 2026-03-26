package protocol

import "os/exec" // want `import of "os/exec" is not allowed`

var _ = exec.Command
