// Command detlint runs the Keystone determinism analyzer.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/logicvoid0x/keystone/tools/detlint"
)

func main() { singlechecker.Main(detlint.Analyzer) }
