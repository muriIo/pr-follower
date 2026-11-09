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

// ValidationReport summarizes the checked ADRs and any violations discovered.
type ValidationReport struct {
	CheckedADRs []string
	Violations  []string
}

var requiredADRSections = []string{
	"# ADR-",
	"## Status",
	"## Context",
	"## Decision",
	"## Consequences",
	"## Compliance & Automated Gate",
}

// ValidateRepository runs all automated Architecture Decision Record compliance checks
// against the given monorepo root directory.
func ValidateRepository(repoRoot string) (*ValidationReport, error) {
	report := &ValidationReport{}

	validateADRCatalog(repoRoot, report)
	validateADR0001CleanArchitecture(repoRoot, report)
	validateADR0002StrictTDD(repoRoot, report)
	validateADR0003And0004GoModAndMigrations(repoRoot, report)
	validateADR0005ZeroClone(repoRoot, report)
	validateADR0007CooperativeCancellation(repoRoot, report)
	validateADR0008And0009FrontendIfPresent(repoRoot, report)

	if len(report.Violations) > 0 {
		return report, fmt.Errorf("found %d ADR compliance violation(s):\n- %s",
			len(report.Violations), strings.Join(report.Violations, "\n- "))
	}

	return report, nil
}

func validateADRCatalog(repoRoot string, report *ValidationReport) {
	adrDir := filepath.Join(repoRoot, "docs", "adr")
	readmePath := filepath.Join(adrDir, "README.md")

	readmeBytes, err := os.ReadFile(readmePath)
	if err != nil {
		report.Violations = append(report.Violations, fmt.Sprintf("ADR Catalog: missing docs/adr/README.md (%v)", err))
		return
	}
	readmeContent := string(readmeBytes)

	entries, err := os.ReadDir(adrDir)
	if err != nil {
		report.Violations = append(report.Violations, fmt.Sprintf("ADR Catalog: cannot read docs/adr directory (%v)", err))
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == "README.md" {
			continue
		}
		name := entry.Name()
		report.CheckedADRs = append(report.CheckedADRs, name)

		if !strings.Contains(readmeContent, name) {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR Catalog: %s is not indexed in docs/adr/README.md", name))
		}

		contentBytes, err := os.ReadFile(filepath.Join(adrDir, name))
		if err != nil {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR Catalog: failed to read %s (%v)", name, err))
			continue
		}
		content := string(contentBytes)
		for _, section := range requiredADRSections {
			if !strings.Contains(content, section) {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR Catalog: %s is missing required section %q", name, section))
			}
		}
	}
	sort.Strings(report.CheckedADRs)
}

func validateADR0001CleanArchitecture(repoRoot string, report *ValidationReport) {
	fset := token.NewFileSet()

	domainDir := filepath.Join(repoRoot, "backend", "internal", "domain")
	_ = filepath.WalkDir(domainDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fileAst, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			report.Violations = append(report.Violations, fmt.Sprintf("ADR-0001: failed to parse %s: %v", path, parseErr))
			return nil
		}
		for _, imp := range fileAst.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if isForbiddenDomainImport(impPath) {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0001: domain file %s imports forbidden package %q", relPath(repoRoot, path), impPath))
			}
		}
		return nil
	})

	usecaseDir := filepath.Join(repoRoot, "backend", "internal", "usecase")
	_ = filepath.WalkDir(usecaseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fileAst, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			report.Violations = append(report.Violations, fmt.Sprintf("ADR-0001: failed to parse %s: %v", path, parseErr))
			return nil
		}
		for _, imp := range fileAst.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if isForbiddenUseCaseImport(impPath) {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0001: usecase file %s imports forbidden outer package %q", relPath(repoRoot, path), impPath))
			}
		}
		return nil
	})
}

func isForbiddenDomainImport(impPath string) bool {
	if strings.Contains(impPath, "/internal/usecase") ||
		strings.Contains(impPath, "/internal/adapter") ||
		strings.Contains(impPath, "/internal/infrastructure") {
		return true
	}
	if impPath == "net/http" || impPath == "database/sql" {
		return true
	}
	// Allow internal/domain imports within the project module, forbid external third-party domains.
	if strings.Contains(impPath, "/internal/domain") {
		return false
	}
	firstSegment := strings.Split(impPath, "/")[0]
	return strings.Contains(firstSegment, ".")
}

func isForbiddenUseCaseImport(impPath string) bool {
	if strings.Contains(impPath, "/internal/adapter") ||
		strings.Contains(impPath, "/internal/infrastructure") {
		return true
	}
	if impPath == "net/http" || impPath == "database/sql" {
		return true
	}
	if strings.Contains(impPath, "/internal/domain") || strings.Contains(impPath, "/internal/usecase") {
		return false
	}
	firstSegment := strings.Split(impPath, "/")[0]
	return strings.Contains(firstSegment, ".")
}

func validateADR0002StrictTDD(repoRoot string, report *ValidationReport) {
	internalDir := filepath.Join(repoRoot, "backend", "internal")
	_ = filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		// Pure interface contracts (port) and generated sqlc code (sqlcgen) are exempt from direct package test files.
		if base == "port" || base == "sqlcgen" {
			return nil
		}
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return nil
		}
		hasProdGo := false
		hasTestGo := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				hasTestGo = true
			} else {
				hasProdGo = true
			}
		}
		if hasProdGo && !hasTestGo {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0002: package %s has production .go files but no *_test.go unit tests", relPath(repoRoot, path)))
		}
		return nil
	})
}

func validateADR0003And0004GoModAndMigrations(repoRoot string, report *ValidationReport) {
	goModPath := filepath.Join(repoRoot, "backend", "go.mod")
	goModBytes, err := os.ReadFile(goModPath)
	if err != nil {
		report.Violations = append(report.Violations, fmt.Sprintf("ADR-0003/ADR-0004: missing backend/go.mod (%v)", err))
		return
	}
	goMod := string(goModBytes)

	// ADR-0003 checks
	forbiddenRouters := []string{
		"github.com/gin-gonic/gin",
		"github.com/labstack/echo",
		"github.com/gofiber/fiber",
	}
	for _, router := range forbiddenRouters {
		if strings.Contains(goMod, router) {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0003: backend/go.mod imports forbidden router framework %q (must use go-chi/chi/v5 + net/http)", router))
		}
	}
	if !strings.Contains(goMod, "github.com/go-chi/chi/v5") {
		report.Violations = append(report.Violations,
			"ADR-0003: backend/go.mod must require github.com/go-chi/chi/v5")
	}

	// ADR-0004 checks
	if strings.Contains(goMod, "gorm.io/gorm") {
		report.Violations = append(report.Violations,
			"ADR-0004: backend/go.mod imports forbidden ORM gorm.io/gorm (must use sqlc + pressly/goose/v3)")
	}
	if !strings.Contains(goMod, "github.com/pressly/goose/v3") {
		report.Violations = append(report.Violations,
			"ADR-0004: backend/go.mod must require github.com/pressly/goose/v3")
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "backend", "sqlc.yaml")); err != nil {
		report.Violations = append(report.Violations,
			"ADR-0004: missing backend/sqlc.yaml configuration")
	}

	migDir := filepath.Join(repoRoot, "backend", "internal", "infrastructure", "database", "migrations")
	entries, err := os.ReadDir(migDir)
	if err != nil {
		report.Violations = append(report.Violations,
			fmt.Sprintf("ADR-0004: missing migrations directory %s", relPath(repoRoot, migDir)))
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		migBytes, readErr := os.ReadFile(filepath.Join(migDir, entry.Name()))
		if readErr != nil {
			continue
		}
		migText := string(migBytes)
		if !strings.Contains(migText, "-- +goose Up") {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0004: migration %s is missing '-- +goose Up'", entry.Name()))
		}
		if strings.Contains(strings.ToLower(migText), "+goose down") {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0004: migration %s contains forbidden '+goose Down' (migrations must be strictly forward-only)", entry.Name()))
		}
	}
}

func validateADR0005ZeroClone(repoRoot string, report *ValidationReport) {
	fset := token.NewFileSet()
	internalDir := filepath.Join(repoRoot, "backend", "internal")
	_ = filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fileAst, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil
		}
		for _, imp := range fileAst.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if impPath == "os/exec" {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0005: %s imports forbidden package \"os/exec\" (zero local git clone rule)", relPath(repoRoot, path)))
			}
		}
		return nil
	})
}

func validateADR0007CooperativeCancellation(repoRoot string, report *ValidationReport) {
	fset := token.NewFileSet()
	usecaseDir := filepath.Join(repoRoot, "backend", "internal", "usecase")
	_ = filepath.WalkDir(usecaseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileAst, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		for _, decl := range fileAst.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			if !hasContextFirstParam(fn) {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0007: exported usecase method %s in %s must accept context.Context as its first parameter",
						fn.Name.Name, relPath(repoRoot, path)))
			}
		}
		return nil
	})
}

func hasContextFirstParam(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	firstParam := fn.Type.Params.List[0]
	sel, ok := firstParam.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "context" && sel.Sel.Name == "Context"
}

func validateADR0008And0009FrontendIfPresent(repoRoot string, report *ValidationReport) {
	frontendPkg := filepath.Join(repoRoot, "frontend", "package.json")
	if _, err := os.Stat(frontendPkg); err != nil {
		// Frontend not scaffolded yet.
		return
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "frontend", ".mcp.json")); err != nil {
		report.Violations = append(report.Violations,
			"ADR-0009: frontend/.mcp.json is required for Angular CLI MCP integration")
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "frontend", "public", "i18n", "pt-BR.json")); err != nil {
		report.Violations = append(report.Violations,
			"ADR-0008: frontend/public/i18n/pt-BR.json default locale file is missing")
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "frontend", "public", "i18n", "en-US.json")); err != nil {
		report.Violations = append(report.Violations,
			"ADR-0008: frontend/public/i18n/en-US.json secondary locale file is missing")
	}

	appDir := filepath.Join(repoRoot, "frontend", "src", "app")
	if _, err := os.Stat(appDir); err != nil {
		return
	}

	// ADR-0001 Frontend Clean Architecture check
	feDomainDir := filepath.Join(appDir, "domain")
	_ = filepath.WalkDir(feDomainDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(raw)
		for _, forbidden := range []string{"/application/", "/infrastructure/", "/presentation/", "../application", "../infrastructure", "../presentation", "@angular/common/http"} {
			if strings.Contains(text, forbidden) {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0001: frontend domain file %s imports forbidden outer module %q", relPath(repoRoot, path), forbidden))
			}
		}
		return nil
	})

	feAppDir := filepath.Join(appDir, "application")
	_ = filepath.WalkDir(feAppDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(raw)
		for _, forbidden := range []string{"/infrastructure/", "/presentation/", "../infrastructure", "../presentation", "@angular/common/http"} {
			if strings.Contains(text, forbidden) {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0001: frontend application file %s imports forbidden outer module %q", relPath(repoRoot, path), forbidden))
			}
		}
		return nil
	})

	// ADR-0002 Frontend TDD check: every directory in src/app with implementation .ts files (excluding pure gateways/config) must have a .spec.ts file
	_ = filepath.WalkDir(appDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if filepath.Base(path) == "gateways" {
			return nil
		}
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return nil
		}
		hasImpl := false
		hasSpec := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".spec.ts") {
				hasSpec = true
			} else if name != "index.ts" && name != "app.config.ts" && name != "app.routes.ts" {
				hasImpl = true
			}
		}
		if hasImpl && !hasSpec {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0002: frontend directory %s has implementation .ts files but no *.spec.ts tests", relPath(repoRoot, path)))
		}
		return nil
	})
}

func relPath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	return rel
}
