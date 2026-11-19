package adrcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

const gooseImportPath = "github.com/pressly/goose/v3"

// Package-level goose functions that apply or revert migrations. They take no lock across
// processes, so two replicas starting together would both migrate (ADR-0016).
var globalGooseMigrators = []string{"Up", "Down", "Run", "Reset", "Redo"}

// validateADR0016LockedMigrations checks that the backend migrates only through a goose provider
// built with goose.WithSessionLocker, never through goose's package-level API.
func validateADR0016LockedMigrations(repoRoot string, report *ValidationReport) {
	root := filepath.Join(repoRoot, "backend", "internal")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		gooseName := importName(file, gooseImportPath, "goose")
		if gooseName == "" {
			return nil
		}
		rel := relPath(repoRoot, path)
		createsProvider, usesSessionLocker := false, false
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != gooseName {
				return true
			}
			switch name := sel.Sel.Name; {
			case name == "NewProvider":
				createsProvider = true
			case name == "WithSessionLocker":
				usesSessionLocker = true
			case isGlobalGooseMigrator(name):
				report.Violations = append(report.Violations, fmt.Sprintf(
					"ADR-0016: %s calls goose.%s, which migrates without a lock across replicas; use goose.NewProvider with goose.WithSessionLocker",
					rel, name))
			}
			return true
		})
		if createsProvider && !usesSessionLocker {
			report.Violations = append(report.Violations, fmt.Sprintf(
				"ADR-0016: %s creates a goose provider without goose.WithSessionLocker; replicas starting together would race through the migrations",
				rel))
		}
		return nil
	})
}

func isGlobalGooseMigrator(name string) bool {
	for _, prefix := range globalGooseMigrators {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// importName returns the name a file uses for importPath (its alias, or fallback when it has
// none), or "" when the file does not import it.
func importName(file *ast.File, importPath, fallback string) string {
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return fallback
	}
	return ""
}
