// Package detlint reports non-deterministic constructs in Keystone state-machine code.
//
// Scope: packages whose import path contains /x/, /app/protocol or /crypto/frost,
// excluding /crypto/frost/secret and /sidecar/. _test.go files and generated files
// (`// Code generated ... DO NOT EDIT.`) are skipped.
//
// Rules:
//  1. float32 / float64 types and values;
//  2. calls to (or references of) time.Now, time.Since, time.Until;
//  3. range over a map, or over maps.Keys / maps.Values / maps.All;
//  4. go statements;
//  5. imports of math/rand, math/rand/v2, crypto/rand, net, net/http, os/exec.
//
// A line opts out with a trailing `//detlint:allow <reason>`. The reason is required.
package detlint

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const allowDirective = "//detlint:allow"

var Analyzer = &analysis.Analyzer{
	Name: "detlint",
	Doc:  "reports non-deterministic constructs in Keystone state-machine code",
	Run:  run,
}

var bannedImports = map[string]bool{
	"math/rand":    true,
	"math/rand/v2": true,
	"crypto/rand":  true,
	"net":          true,
	"net/http":     true,
	"os/exec":      true,
}

var bannedTimeFuncs = map[string]bool{"Now": true, "Since": true, "Until": true}

var mapIterFuncs = map[string]bool{"Keys": true, "Values": true, "All": true}

// InScope reports whether detlint applies to the package with the given import path.
func InScope(pkgPath string) bool {
	p := "/" + strings.TrimSuffix(pkgPath, "_test") + "/"
	if strings.Contains(p, "/sidecar/") || strings.Contains(p, "/crypto/frost/secret/") {
		return false
	}
	return strings.Contains(p, "/x/") || strings.Contains(p, "/app/protocol/") ||
		strings.Contains(p, "/crypto/frost/")
}

type checker struct {
	pass    *analysis.Pass
	allowed map[int]bool // lines carrying a valid allow directive, for the current file
	file    *token.File
	seen    map[string]bool // one report per (line, rule)
}

func run(pass *analysis.Pass) (any, error) {
	if !InScope(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, f := range pass.Files {
		tf := pass.Fset.File(f.Pos())
		// Generated code (protobuf, grpc-gateway) carries no state-machine logic.
		if tf == nil || strings.HasSuffix(tf.Name(), "_test.go") || ast.IsGenerated(f) {
			continue
		}
		c := &checker{pass: pass, file: tf, allowed: map[int]bool{}, seen: map[string]bool{}}
		c.collectAllows(f)
		c.checkFile(f)
	}
	return nil, nil
}

func (c *checker) collectAllows(f *ast.File) {
	for _, cg := range f.Comments {
		for _, cm := range cg.List {
			if !strings.HasPrefix(cm.Text, allowDirective) {
				continue
			}
			rest := strings.TrimPrefix(cm.Text, allowDirective)
			if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
				continue // some other directive, e.g. //detlint:allowfoo
			}
			if i := strings.Index(rest, "//"); i >= 0 {
				rest = rest[:i] // a trailing comment is not a reason
			}
			if strings.TrimSpace(rest) == "" {
				c.pass.Reportf(cm.Pos(), "detlint:allow requires a reason")
				continue
			}
			c.allowed[c.file.Line(cm.Pos())] = true
		}
	}
}

func (c *checker) report(pos token.Pos, rule, format string, args ...any) {
	line := c.file.Line(pos)
	if c.allowed[line] {
		return
	}
	key := strconv.Itoa(line) + "/" + rule
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.pass.Reportf(pos, format, args...)
}

func (c *checker) checkFile(f *ast.File) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err == nil && bannedImports[path] {
			c.report(imp.Pos(), "import", "import of %q is not allowed in state-machine code", path)
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.GoStmt:
			c.report(n.Pos(), "go", "go statement is not allowed in state-machine code")
		case *ast.RangeStmt:
			c.checkRange(n)
		case *ast.Ident:
			c.checkIdent(n)
		}
		if e, ok := n.(ast.Expr); ok {
			if tv, ok := c.pass.TypesInfo.Types[e]; ok && isFloat(tv.Type) {
				c.report(e.Pos(), "float", "floating-point type %s is not allowed in state-machine code", tv.Type)
			}
		}
		return true
	})
}

func (c *checker) checkIdent(id *ast.Ident) {
	if obj := c.pass.TypesInfo.Defs[id]; obj != nil {
		if v, ok := obj.(*types.Var); ok && isFloat(v.Type()) {
			c.report(id.Pos(), "float", "floating-point type %s is not allowed in state-machine code", v.Type())
		}
	}
	fn, ok := c.pass.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return
	}
	if fn.Pkg().Path() == "time" && bannedTimeFuncs[fn.Name()] {
		c.report(id.Pos(), "time", "time.%s is not allowed in state-machine code; use the block time", fn.Name())
	}
}

func (c *checker) checkRange(r *ast.RangeStmt) {
	if tv, ok := c.pass.TypesInfo.Types[r.X]; ok && tv.Type != nil {
		if _, isMap := tv.Type.Underlying().(*types.Map); isMap {
			c.report(r.Pos(), "range", "range over a map has random order; iterate a sorted slice or collections")
			return
		}
	}
	call, ok := ast.Unparen(r.X).(*ast.CallExpr)
	if !ok {
		return
	}
	var id *ast.Ident
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		id = fun.Sel
	case *ast.Ident:
		id = fun
	}
	if id == nil {
		return
	}
	if fn, ok := c.pass.TypesInfo.Uses[id].(*types.Func); ok && fn.Pkg() != nil &&
		fn.Pkg().Path() == "maps" && mapIterFuncs[fn.Name()] {
		c.report(r.Pos(), "range", "range over maps.%s has random order; sort the keys first", fn.Name())
	}
}

func isFloat(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Kind() == types.Float32 || b.Kind() == types.Float64
}
