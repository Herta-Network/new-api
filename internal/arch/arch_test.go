// Package arch checks the boundaries between the shared kernel and the
// modules under internal/module; see "Modules" in AGENTS.md.
package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/QuantumNous/new-api/"

// entryPoints may use module facades; router also wires module transports.
var entryPoints = []string{".", "controller", "router"}

// tableOwners maps each module table to the module that keeps it.
var tableOwners = map[string]string{
	"payment_methods":        "payment",
	"payment_orders":         "payment",
	"payment_unmatched":      "payment",
	"payment_exchange_rates": "currency",
	"email_templates":        "mail",
}

// skippedDirs are not Go code of the root module.
var skippedDirs = map[string]bool{"web": true, "relaykit": true, "tokenkit": true, "docs": true, "plugins": true, "bin": true}

type goFile struct {
	dir     string // repository-relative package directory
	path    string // repository-relative file path
	test    bool
	imports []string // root-module imports, repository-relative
	strings []string // string literals
}

func collectGoFiles(t *testing.T) []goFile {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	var files []goFile
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (skippedDirs[rel] || strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		file := goFile{dir: filepath.ToSlash(filepath.Dir(rel)), path: rel, test: strings.HasSuffix(rel, "_test.go")}
		for _, spec := range parsed.Imports {
			value, _ := strconv.Unquote(spec.Path.Value)
			if imported, ok := strings.CutPrefix(value, modulePath); ok {
				file.imports = append(file.imports, imported)
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				file.strings = append(file.strings, literal.Value)
			}
			return true
		})
		files = append(files, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	return files
}

// within reports whether pkg is dir or nested under it.
func within(pkg, dir string) bool {
	return pkg == dir || strings.HasPrefix(pkg, dir+"/")
}

// moduleOf splits internal/module/<name>/<rest> into name and rest.
func moduleOf(pkg string) (name string, rest string, ok bool) {
	tail, ok := strings.CutPrefix(pkg, "internal/module/")
	if !ok {
		return "", "", false
	}
	name, rest, _ = strings.Cut(tail, "/")
	return name, rest, true
}

// isPublicPart reports a module package other code may import: the facade
// or the entity package.
func isPublicPart(rest string) bool {
	return rest == "" || within(rest, "entity")
}

// A module exposes its facade, entity/ and transport/; its implementation
// lives under internal/. contract/ and events/ are reserved for module
// types and integration events once another module needs them.
func TestModuleLayout(t *testing.T) {
	allowed := []string{"internal", "entity", "transport", "contract", "events"}
	for _, file := range collectGoFiles(t) {
		name, rest, ok := moduleOf(file.dir)
		if !ok || rest == "" {
			continue
		}
		top, _, _ := strings.Cut(rest, "/")
		if !slices.Contains(allowed, top) {
			t.Errorf("%s: module %s may only have its facade, entity/, transport/ and internal/ packages; move it under internal/module/%s/internal/", file.path, name, name)
		}
	}
}

// The shared kernel never depends on a module. Tests may import a module's
// entity package, as the migration check of the model package does.
func TestKernelDoesNotImportModules(t *testing.T) {
	for _, file := range collectGoFiles(t) {
		if within(file.dir, "internal") || slices.Contains(entryPoints, file.dir) {
			continue
		}
		for _, imported := range file.imports {
			_, rest, ok := moduleOf(imported)
			if !ok || (file.test && within(rest, "entity")) {
				continue
			}
			t.Errorf("%s: the shared kernel imports module package %s; call the module from controller, router or main instead", file.path, imported)
		}
	}
}

// Code outside a module reaches it through its facade and entity package;
// only the router wires its transport.
func TestModuleImportsStayAtFacades(t *testing.T) {
	for _, file := range collectGoFiles(t) {
		owner, _, inModule := moduleOf(file.dir)
		for _, imported := range file.imports {
			name, rest, ok := moduleOf(imported)
			if !ok || (inModule && name == owner) || isPublicPart(rest) {
				continue
			}
			if within(rest, "transport") && file.dir == "router" {
				continue
			}
			t.Errorf("%s: imports %s; outside module %s use its facade or entity package", file.path, imported, name)
		}
	}
}

// Modules are called by the entry points, never the other way around.
func TestModulesDoNotImportEntryPoints(t *testing.T) {
	for _, file := range collectGoFiles(t) {
		if !within(file.dir, "internal/module") {
			continue
		}
		for _, imported := range file.imports {
			if within(imported, "controller") || within(imported, "router") {
				t.Errorf("%s: a module imports entry point %s", file.path, imported)
			}
		}
	}
}

// A module's handlers adapt its facade; they do not reach into its
// implementation.
func TestTransportUsesOwnFacade(t *testing.T) {
	for _, file := range collectGoFiles(t) {
		owner, rest, ok := moduleOf(file.dir)
		if !ok || !within(rest, "transport") {
			continue
		}
		for _, imported := range file.imports {
			name, importedRest, ok := moduleOf(imported)
			if ok && name == owner && within(importedRest, "internal") {
				t.Errorf("%s: transport imports %s; expose what it needs on the %s facade", file.path, imported, owner)
			}
		}
	}
}

// A module table is only queried by its module. Tests may set fixtures up
// through their own queries.
func TestModuleTablesStayWithOwner(t *testing.T) {
	for _, file := range collectGoFiles(t) {
		if file.test {
			continue
		}
		owner, _, _ := moduleOf(file.dir)
		for _, literal := range file.strings {
			for table, tableOwner := range tableOwners {
				if tableOwner != owner && strings.Contains(literal, table) {
					t.Errorf("%s: names table %s, which module %s keeps; ask its facade instead", file.path, table, tableOwner)
				}
			}
		}
	}
}
