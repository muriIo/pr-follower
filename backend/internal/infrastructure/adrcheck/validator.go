package adrcheck

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	validateADR0010GitHubOnlyIdentity(repoRoot, report)
	validateADR0011IdempotentIntegrations(repoRoot, report)

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

	validateADR0008TemplateTranslationKeys(repoRoot, appDir, report)

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

	// ADR-0009 Presentation Componentization check:
	// If presentation/ exists, it must be organized into common/, features/, and layout/,
	// and no single *.component.html template may exceed 220 lines (no monolithic shell templates).
	presentationDir := filepath.Join(appDir, "presentation")
	if _, err := os.Stat(presentationDir); err == nil {
		for _, sub := range []string{"common", "features", "layout"} {
			if _, statErr := os.Stat(filepath.Join(presentationDir, sub)); statErr != nil {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0009: frontend presentation layer is missing required modular directory frontend/src/app/presentation/%s", sub))
			}
		}
		_ = filepath.WalkDir(presentationDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".component.html") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			lineCount := strings.Count(string(raw), "\n") + 1
			if lineCount > 220 {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0009: monolithic component template %s has %d lines (max 220; split into common/ and features/ components)",
						relPath(repoRoot, path), lineCount))
			}
			return nil
		})
	}

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

var (
	translatePipePattern  = regexp.MustCompile(`\|\s*t\b`)
	translationKeyLiteral = regexp.MustCompile(`'([A-Za-z][\w-]*(?:\.[\w-]+)+)'`)
)

// validateADR0008TemplateTranslationKeys reports literal keys passed to the `t` pipe in any
// template that are missing from pt-BR.json or en-US.json (an untranslated key renders raw).
func validateADR0008TemplateTranslationKeys(repoRoot, appDir string, report *ValidationReport) {
	dictionaries := map[string]map[string]any{}
	for _, locale := range []string{"pt-BR", "en-US"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, "frontend", "public", "i18n", locale+".json"))
		if err != nil {
			return
		}
		dict := map[string]any{}
		if err := json.Unmarshal(raw, &dict); err != nil {
			report.Violations = append(report.Violations, fmt.Sprintf("ADR-0008: %s.json is not valid JSON: %v", locale, err))
			return
		}
		dictionaries[locale] = dict
	}

	_ = filepath.WalkDir(appDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, key := range templateTranslationKeys(string(raw)) {
			for _, locale := range []string{"pt-BR", "en-US"} {
				if _, ok := dictionaries[locale][key]; !ok {
					report.Violations = append(report.Violations,
						fmt.Sprintf("ADR-0008: translation key %q used in %s is missing from %s.json", key, relPath(repoRoot, path), locale))
				}
			}
		}
		return nil
	})
}

// templateTranslationKeys returns the quoted keys in the expression before each `| t`, back to
// the start of its interpolation ({{) or attribute value ("), so `(a ? 'x' : 'y') | t` yields both.
func templateTranslationKeys(template string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, loc := range translatePipePattern.FindAllStringIndex(template, -1) {
		before := template[:loc[0]]
		start := max(strings.LastIndex(before, "{{"), strings.LastIndex(before, `"`))
		expr := before[start+1:]
		for _, m := range translationKeyLiteral.FindAllStringSubmatch(expr, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				keys = append(keys, m[1])
			}
		}
	}
	return keys
}

var (
	createStatementPattern = regexp.MustCompile(`(?i)CREATE\s+(TABLE|(?:UNIQUE\s+)?INDEX)\s+(IF\s+NOT\s+EXISTS)?`)
	queryNamePattern       = regexp.MustCompile(`--\s*name:\s*(\w+)`)
)

// validateADR0011IdempotentIntegrations checks the mechanical parts of ADR-0011.
func validateADR0011IdempotentIntegrations(repoRoot string, report *ValidationReport) {
	internal := filepath.Join(repoRoot, "backend", "internal")

	// 1. Inserts are upserts, or explain why a duplicate cannot happen.
	if raw, err := os.ReadFile(filepath.Join(internal, "adapter", "persistence", "queries", "queries.sql")); err == nil {
		blocks := strings.Split(string(raw), "-- name:")
		for _, block := range blocks[1:] {
			block = "-- name:" + block
			upper := strings.ToUpper(block)
			if !strings.Contains(upper, "INSERT INTO") || strings.Contains(upper, "ON CONFLICT") || strings.Contains(block, "-- idempotent:") {
				continue
			}
			name := "?"
			if m := queryNamePattern.FindStringSubmatch(block); m != nil {
				name = m[1]
			}
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0011: query %s inserts without ON CONFLICT or an \"-- idempotent:\" note", name))
		}
	}

	// 2. Schema statements are re-runnable.
	migDir := filepath.Join(internal, "infrastructure", "database", "migrations")
	if entries, err := os.ReadDir(migDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			raw, readErr := os.ReadFile(filepath.Join(migDir, entry.Name()))
			if readErr != nil {
				continue
			}
			for _, m := range createStatementPattern.FindAllStringSubmatch(string(raw), -1) {
				if strings.TrimSpace(m[2]) == "" {
					kind := "CREATE TABLE"
					if !strings.EqualFold(m[1], "TABLE") {
						kind = "CREATE INDEX"
					}
					report.Violations = append(report.Violations,
						fmt.Sprintf("ADR-0011: %s: %s without IF NOT EXISTS", entry.Name(), kind))
				}
			}
		}
	}

	// 3. Non-idempotent HTTP calls are not retried blindly.
	fset := token.NewFileSet()
	_ = filepath.WalkDir(filepath.Join(internal, "adapter"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSelector(unwrapIndex(call.Fun), "resilience", "Retry") {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.FuncLit)
				if !ok {
					continue
				}
				ast.Inspect(lit, func(inner ast.Node) bool {
					for _, method := range []string{"MethodPost", "MethodPatch"} {
						if isSelector(inner, "http", method) {
							report.Violations = append(report.Violations,
								fmt.Sprintf("ADR-0011: %s retries an http.%s request with resilience.Retry; use resilience.RetryNonIdempotent", relPath(repoRoot, path), method))
						}
					}
					return true
				})
			}
			return true
		})
		return nil
	})

	// 4. Every webhook ingest method has a redelivery test.
	webhookDir := filepath.Join(internal, "usecase", "webhook")
	entries, err := os.ReadDir(webhookDir)
	if err != nil {
		return
	}
	var ingestMethods []string
	covered := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(webhookDir, entry.Name()), nil, 0)
		if parseErr != nil {
			continue
		}
		isTest := strings.HasSuffix(entry.Name(), "_test.go")
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if !isTest && fn.Recv != nil && fn.Name.IsExported() && strings.HasPrefix(fn.Name.Name, "Ingest") {
				ingestMethods = append(ingestMethods, fn.Name.Name)
			}
			if isTest && strings.HasPrefix(fn.Name.Name, "Test") && strings.Contains(fn.Name.Name, "Redeliver") && fn.Body != nil {
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok {
						covered[sel.Sel.Name] = true
					}
					return true
				})
			}
		}
	}
	sort.Strings(ingestMethods)
	for _, method := range ingestMethods {
		if !covered[method] {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0011: webhook ingest method %s has no test named *Redeliver* proving a repeated delivery is a no-op", method))
		}
	}
}

func unwrapIndex(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return e.X
	case *ast.IndexListExpr:
		return e.X
	}
	return expr
}

func isSelector(node ast.Node, pkg, name string) bool {
	sel, ok := node.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// forbiddenIdentityHeader is assembled so this package does not trip its own scan.
var forbiddenIdentityHeader = "X-User-" + "ID"

func validateADR0010GitHubOnlyIdentity(repoRoot string, report *ValidationReport) {
	scanRoots := []string{
		filepath.Join(repoRoot, "backend", "internal"),
		filepath.Join(repoRoot, "frontend", "src", "app"),
	}
	for _, scanRoot := range scanRoots {
		_ = filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "adrcheck" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			isGo := strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
			isTS := strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".spec.ts")
			if !isGo && !isTS {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr == nil && strings.Contains(string(raw), forbiddenIdentityHeader) {
				report.Violations = append(report.Violations,
					fmt.Sprintf("ADR-0010: %s references the %s header; identity must come from the session", relPath(repoRoot, path), forbiddenIdentityHeader))
			}
			return nil
		})
	}

	migDir := filepath.Join(repoRoot, "backend", "internal", "infrastructure", "database", "migrations")
	entries, err := os.ReadDir(migDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(migDir, entry.Name()))
		if readErr == nil && strings.Contains(strings.ToLower(string(raw)), "password") {
			report.Violations = append(report.Violations,
				fmt.Sprintf("ADR-0010: migration %s defines a password column; GitHub is the only identity provider", entry.Name()))
		}
	}
}

func relPath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	return rel
}
