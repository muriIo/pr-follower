package adrcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The only places that may call CreateNotifications: the notifier, which decides what deserves a
// notification, and the store that implements it (ADR-0017).
var notificationCreators = []string{
	filepath.Join("backend", "internal", "usecase", "notify"),
	filepath.Join("backend", "internal", "adapter", "persistence"),
}

// validateADR0017NotificationsFromTheAPI checks that notifications are created only by the
// notifier and that every delivery channel adapter has a redelivery test.
func validateADR0017NotificationsFromTheAPI(repoRoot string, report *ValidationReport) {
	internal := filepath.Join(repoRoot, "backend", "internal")
	_ = filepath.WalkDir(internal, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := relPath(repoRoot, path)
		for _, allowed := range notificationCreators {
			if strings.HasPrefix(filepath.FromSlash(rel), allowed+string(filepath.Separator)) {
				return nil
			}
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CreateNotifications" {
				report.Violations = append(report.Violations, fmt.Sprintf(
					"ADR-0017: %s creates notifications; only usecase/notify decides that a notification exists",
					rel))
				return false
			}
			return true
		})
		return nil
	})

	channels := filepath.Join(internal, "adapter", "notifychannel")
	entries, err := os.ReadDir(channels)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(channels, entry.Name())
		if !hasRedeliverTest(dir) {
			report.Violations = append(report.Violations, fmt.Sprintf(
				"ADR-0017: delivery channel %s has no *Redeliver* test; the same notification handed over twice must reach the user once",
				relPath(repoRoot, dir)))
		}
	}
}

func hasRedeliverTest(dir string) bool {
	tests, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
	for _, path := range tests {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Test") && strings.Contains(fn.Name.Name, "Redeliver") {
				return true
			}
		}
	}
	return false
}
