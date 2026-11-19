package adrcheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/infrastructure/adrcheck"
)

func TestValidateRepository_LiveMonorepo(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	report, err := adrcheck.ValidateRepository(repoRoot)
	if err != nil {
		t.Fatalf("expected live repository to pass all ADR gates, got error: %v\nviolations: %v", err, report.Violations)
	}
	if len(report.CheckedADRs) < 9 {
		t.Fatalf("expected at least 9 ADRs checked, got %d: %v", len(report.CheckedADRs), report.CheckedADRs)
	}
}

func TestValidateRepository_DetectsAllADRViolations(t *testing.T) {
	t.Parallel()

	t.Run("detects missing ADR section or unindexed ADR file", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		// Write an ADR missing the required "## Compliance & Automated Gate" section and not listed in README.md.
		badADR := filepath.Join(root, "docs", "adr", "0010-incomplete.md")
		if err := os.WriteFile(badADR, []byte("# ADR-0010: Incomplete\n\n## Status\nAccepted\n"), 0o644); err != nil {
			t.Fatalf("write bad ADR: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected error for incomplete and unindexed ADR, got nil")
		}
		joined := strings.Join(report.Violations, "\n")
		if !strings.Contains(joined, "0010-incomplete.md") {
			t.Fatalf("expected violation mentioning 0010-incomplete.md, got: %s", joined)
		}
	})

	t.Run("ADR-0001: detects forbidden import in domain layer", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		badDomainFile := filepath.Join(root, "backend", "internal", "domain", "entity", "bad.go")
		content := `package entity
import "net/http"
var _ = http.StatusOK
`
		if err := os.WriteFile(badDomainFile, []byte(content), 0o644); err != nil {
			t.Fatalf("write bad domain file: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0001 violation error, got nil")
		}
		if !containsViolation(report.Violations, "ADR-0001") {
			t.Fatalf("expected ADR-0001 violation, got: %v", report.Violations)
		}
	})

	t.Run("ADR-0002: detects production package without unit tests", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		untestedDir := filepath.Join(root, "backend", "internal", "usecase", "untested")
		if err := os.MkdirAll(untestedDir, 0o755); err != nil {
			t.Fatalf("mkdir untested: %v", err)
		}
		content := `package untested
import "context"
type Service struct{}
func (s *Service) Execute(ctx context.Context) error { return nil }
`
		if err := os.WriteFile(filepath.Join(untestedDir, "service.go"), []byte(content), 0o644); err != nil {
			t.Fatalf("write untested service.go: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0002 violation error, got nil")
		}
		if !containsViolation(report.Violations, "ADR-0002") {
			t.Fatalf("expected ADR-0002 violation, got: %v", report.Violations)
		}
	})

	t.Run("ADR-0003 & ADR-0004: detects forbidden framework/ORM or +goose Down migration", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		// Add gorm.io/gorm and gin-gonic/gin to go.mod
		goModPath := filepath.Join(root, "backend", "go.mod")
		if err := os.WriteFile(goModPath, []byte("module example.com/backend\ngo 1.27.3\nrequire (\n\tgithub.com/gin-gonic/gin v1.10.0\n\tgorm.io/gorm v1.25.0\n)\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}

		// Add +goose Down to migration
		migPath := filepath.Join(root, "backend", "internal", "infrastructure", "database", "migrations", "00001_init.sql")
		if err := os.WriteFile(migPath, []byte("-- +goose Up\nCREATE TABLE t (id INT);\n-- +goose Down\nDROP TABLE t;\n"), 0o644); err != nil {
			t.Fatalf("write migration: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0003 and ADR-0004 violations, got nil")
		}
		if !containsViolation(report.Violations, "ADR-0003") {
			t.Fatalf("expected ADR-0003 violation, got: %v", report.Violations)
		}
		if !containsViolation(report.Violations, "ADR-0004") {
			t.Fatalf("expected ADR-0004 violation, got: %v", report.Violations)
		}
	})

	t.Run("ADR-0005: detects forbidden os/exec import in backend internal packages", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		badAdapterFile := filepath.Join(root, "backend", "internal", "adapter", "github", "clone.go")
		content := `package github
import "os/exec"
func BadClone() *exec.Cmd { return exec.Command("git", "clone") }
`
		if err := os.WriteFile(badAdapterFile, []byte(content), 0o644); err != nil {
			t.Fatalf("write bad adapter file: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0005 violation, got nil")
		}
		if !containsViolation(report.Violations, "ADR-0005") {
			t.Fatalf("expected ADR-0005 violation, got: %v", report.Violations)
		}
	})

	t.Run("ADR-0007: detects exported usecase method missing context.Context first parameter", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		ucFile := filepath.Join(root, "backend", "internal", "usecase", "triage", "triage.go")
		content := `package triage
type UseCase struct{}
func (u *UseCase) ExecuteWithoutContext(id uint64) error { return nil }
`
		if err := os.WriteFile(ucFile, []byte(content), 0o644); err != nil {
			t.Fatalf("write bad usecase file: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0007 violation, got nil")
		}
		if !containsViolation(report.Violations, "ADR-0007") {
			t.Fatalf("expected ADR-0007 violation, got: %v", report.Violations)
		}
	})

	t.Run("ADR-0001 frontend & ADR-0002/0008/0009 frontend checks", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		// Create frontend/package.json without .mcp.json or i18n files, plus a domain file importing presentation without a spec
		feDomainDir := filepath.Join(root, "frontend", "src", "app", "domain")
		if err := os.MkdirAll(feDomainDir, 0o755); err != nil {
			t.Fatalf("mkdir frontend domain: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "frontend", "package.json"), []byte(`{"name":"frontend"}`), 0o644); err != nil {
			t.Fatalf("write package.json: %v", err)
		}
		if err := os.WriteFile(filepath.Join(feDomainDir, "bad.ts"), []byte("import { Foo } from '../presentation/foo';\nexport const x = Foo;\n"), 0o644); err != nil {
			t.Fatalf("write bad.ts: %v", err)
		}

		// Also create a monolithic presentation component without common/features/layout
		fePresDir := filepath.Join(root, "frontend", "src", "app", "presentation", "shell")
		if err := os.MkdirAll(fePresDir, 0o755); err != nil {
			t.Fatalf("mkdir frontend presentation: %v", err)
		}
		bigTemplate := strings.Repeat("<div>line</div>\n", 230)
		if err := os.WriteFile(filepath.Join(fePresDir, "shell.component.html"), []byte(bigTemplate), 0o644); err != nil {
			t.Fatalf("write shell.component.html: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0001, ADR-0002, ADR-0008, and ADR-0009 violations, got nil")
		}
		for _, code := range []string{"ADR-0001", "ADR-0002", "ADR-0008", "ADR-0009"} {
			if !containsViolation(report.Violations, code) {
				t.Fatalf("expected %s violation, got: %v", code, report.Violations)
			}
		}
	})

	t.Run("ADR-0008: detects template translation keys missing from a dictionary", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		fe := filepath.Join(root, "frontend")
		files := map[string]string{
			"package.json":           `{"name":"frontend"}`,
			".mcp.json":              `{"mcpServers":{"angular-cli":{"command":"npx","args":["-y","@angular/cli","mcp"]}}}`,
			"public/i18n/pt-BR.json": `{"app.brand":"PR Follower","only.pt":"x"}`,
			"public/i18n/en-US.json": `{"app.brand":"PR Follower","only.pt":"x"}`,
			"src/app/presentation/common/card/card.component.html": "<strong>{{ 'app.title' | t }}</strong>\n" +
				"<span>{{ 'app.brand' | t }}</span>\n" +
				"<button [title]=\"'card.hint' | t\">{{ (busy() ? 'card.busy' : 'card.idle') | t }}</button>\n",
		}
		for rel, content := range files {
			path := filepath.Join(fe, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected missing translation keys to be reported")
		}
		for _, key := range []string{"app.title", "card.hint", "card.busy", "card.idle"} {
			if !containsViolation(report.Violations, "ADR-0008: translation key \""+key+"\"") {
				t.Errorf("expected missing key %q to be reported, got: %v", key, report.Violations)
			}
		}
		if containsViolation(report.Violations, "\"app.brand\"") {
			t.Errorf("app.brand exists in both dictionaries and must not be reported: %v", report.Violations)
		}
	})

	t.Run("ADR-0008: detects hardcoded display text outside the dictionaries", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		fe := filepath.Join(root, "frontend")
		files := map[string]string{
			"package.json":           `{"name":"frontend"}`,
			".mcp.json":              `{"mcpServers":{"angular-cli":{"command":"npx","args":["-y","@angular/cli","mcp"]}}}`,
			"public/i18n/pt-BR.json": `{"k.ok":"ok"}`,
			"public/i18n/en-US.json": `{"k.ok":"ok"}`,
			"src/app/application/stores/store.ts": "const blocked = 'Mesclagem bloqueada: o CI ainda está rodando.';\n" +
				"const label = `Todos (${n})`;\n" +
				"const key = 'drawer.blocked.conflict';\n" +
				"const css = 'pf-dot pf-dot-ok';\n",
			"src/app/application/stores/locale.store.ts": "const DEFAULT = { 'k.ok': 'Você aprovou' };\n",
			"src/app/testing/fixtures.ts":                "const fixture = 'Você aprovou #1';\n",
			"src/app/application/stores/store.spec.ts":   "expect(x).toBe('Você aprovou');\n",
			"src/app/presentation/common/card/card.component.html": "<p>Este PR foi mesclado</p>\n" +
				"<span>{{ 'k.ok' | t }}</span>\n" +
				"<app-badge label=\"Usa API que quebra\" />\n" +
				"<app-badge [label]=\"'k.ok' | t\" />\n",
		}
		for rel, content := range files {
			path := filepath.Join(fe, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected hardcoded text to be reported")
		}
		joined := strings.Join(report.Violations, "\n")
		for _, want := range []string{
			"store.ts:1 hardcoded text \"Mesclagem bloqueada",
			"store.ts:2 hardcoded text \"Todos (${n})\"",
			"card.component.html raw text \"Este PR foi mesclado\"",
			"card.component.html literal label=\"Usa API que quebra\"",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("expected violation containing %q, got:\n%s", want, joined)
			}
		}
		for _, allowed := range []string{"store.ts:3", "store.ts:4", "locale.store.ts", "fixtures.ts", "store.spec.ts"} {
			if strings.Contains(joined, allowed) {
				t.Errorf("%s is allowed and must not be reported:\n%s", allowed, joined)
			}
		}
	})

	t.Run("ADR-0012: detects in-memory shared state that does not say why it can be per replica", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		dir := filepath.Join(root, "backend", "internal", "adapter", "jobs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		files := map[string]string{
			"jobs.go": `package jobs

import "sync"

// Tracker keeps every job's progress.
type Tracker struct {
	mu   sync.Mutex
	jobs map[string]int
}

// Cache memoizes reads.
//
// replica-local: a cache; a miss on another replica only costs a read.
type Cache struct {
	mu sync.RWMutex
}

type (
	// Counter counts things.
	Counter struct {
		mu *sync.Mutex
	}
)

// Plain has no lock.
type Plain struct{ n int }
`,
			"jobs_test.go": "package jobs\nimport \"testing\"\nfunc TestJobs(t *testing.T) {}\n",
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0012 violations, got nil")
		}
		joined := strings.Join(report.Violations, "\n")
		for _, want := range []string{"ADR-0012: type Tracker", "ADR-0012: type Counter"} {
			if !strings.Contains(joined, want) {
				t.Errorf("expected a violation containing %q, got:\n%s", want, joined)
			}
		}
		for _, allowed := range []string{"type Cache", "type Plain"} {
			if strings.Contains(joined, allowed) {
				t.Errorf("%s must not be reported:\n%s", allowed, joined)
			}
		}
	})

	t.Run("ADR-0013: detects unclassified GitHub errors and codes the SPA cannot show", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		files := map[string]string{
			"backend/internal/adapter/github/reads.go": `package github

import (
	"errors"
	"fmt"

	"example/entity"
)

func bare(status int) error    { return fmt.Errorf("github server error (%d)", status) }
func plain() error             { return errors.New("github said no") }
func wrapped(status int) error { return fmt.Errorf("%w: github server error (%d)", entity.ErrGitHubUnavailable, status) }
`,
			"backend/internal/adapter/github/reads_test.go": "package github\nimport \"errors\"\nvar _ = errors.New(\"tests may\")\n",
			"backend/internal/adapter/http/handler.go": `package httpadapter

func statusAndCode(err error) (int, string) {
	status, code := 500, "internal"
	switch {
	case err == nil:
		status, code = 409, "conflict"
	case err != nil:
		status, code = 502, "github_unavailable"
	}
	return status, code
}

var unconfigured = map[string]string{"error": "x", "code": "brand_new"}
`,
			"backend/internal/adapter/http/handler_test.go":                   "package httpadapter\nimport \"testing\"\nfunc TestHandler(t *testing.T) {}\n",
			"frontend/src/app/domain/models/action-error.models.ts":           "const KNOWN_CODES: readonly string[] = [\n  'internal',\n  'conflict',\n  'forbidden',\n];\n",
			"frontend/public/i18n/pt-BR.json":                                 `{"actions.error.internal": "Erro", "actions.error.conflict": "Conflito"}`,
			"frontend/public/i18n/en-US.json":                                 `{"actions.error.internal": "Error", "actions.error.conflict": "Conflict", "actions.error.forbidden": "Denied"}`,
			"frontend/src/app/infrastructure/http/http-thing.gateway.ts":      "export async function get(): Promise<void> {\n  throw new Error('unavailable');\n}\n",
			"frontend/src/app/infrastructure/http/http-thing.gateway.spec.ts": "it('x', () => { throw new Error('specs may'); });\n",
		}
		for rel, content := range files {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0013 violations")
		}
		joined := strings.Join(report.Violations, "\n")
		for _, want := range []string{
			"ADR-0013: backend/internal/adapter/github/reads.go:10",
			"ADR-0013: backend/internal/adapter/github/reads.go:11",
			`ADR-0013: the API sends code "github_unavailable"`,
			`ADR-0013: the API sends code "brand_new"`,
			`ADR-0013: code "forbidden" has no "actions.error.forbidden" in pt-BR.json`,
			"ADR-0013: frontend/src/app/infrastructure/http/http-thing.gateway.ts throws a plain Error",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("expected violation %q, got:\n%s", want, joined)
			}
		}
		for _, unwanted := range []string{"reads.go:12", "reads_test.go", `code "internal"`, `code "conflict"`, `"forbidden" has no "actions.error.forbidden" in en-US.json`, "spec.ts"} {
			if strings.Contains(joined, unwanted) {
				t.Errorf("%s is compliant and must not be reported:\n%s", unwanted, joined)
			}
		}
	})

	adr0015Login := func(body string) string {
		return `package auth

import "context"

type UseCase struct{ users, sessions store }
type store interface{ Upsert(context.Context) error; Create(context.Context) error }

func (uc *UseCase) requireAllowedOrgMember(ctx context.Context) error { return nil }

func (uc *UseCase) CompleteLogin(ctx context.Context) error {
` + body + `
}
`
	}
	adr0015Files := func(login, callbackCodes, known, ptBR, enUS, docs string) map[string]string {
		return map[string]string{
			"backend/internal/usecase/auth/auth.go":                     login,
			"backend/internal/usecase/auth/auth_test.go":                "package auth\n",
			"backend/internal/adapter/http/auth.go":                     "package httpadapter\n\nfunc cb() {\n" + callbackCodes + "}\n",
			"backend/internal/adapter/http/auth_test.go":                "package httpadapter\n",
			"backend/internal/infrastructure/config/config.go":          "package config\n\nconst allowlist = \"ALLOWED_GITHUB_ORGS\"\n",
			"backend/internal/infrastructure/config/config_test.go":     "package config\n",
			"frontend/src/app/infrastructure/auth/http-auth.gateway.ts": "const KNOWN_AUTH_ERRORS: readonly AuthErrorCode[] = [\n" + known + "];\n",
			"frontend/public/i18n/pt-BR.json":                           ptBR,
			"frontend/public/i18n/en-US.json":                           enUS,
			"docs/production-setup.md":                                  docs,
			".env.example":                                              docs,
		}
	}

	t.Run("ADR-0015: a sign-in that checks membership first and names every refusal passes", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, root, adr0015Files(
			adr0015Login("\tif err := uc.requireAllowedOrgMember(ctx); err != nil {\n\t\treturn err\n\t}\n\t_ = uc.users.Upsert(ctx)\n\treturn uc.sessions.Create(ctx)"),
			"\tredirectWithAuthError(w, r, \"not_org_member\")\n",
			"  'not_org_member',\n",
			`{"auth.error.not_org_member": "Não"}`,
			`{"auth.error.not_org_member": "No"}`,
			"Set ALLOWED_GITHUB_ORGS=acme\n",
		))
		report, err := adrcheck.ValidateRepository(root)
		if err != nil || containsViolation(report.Violations, "ADR-0015") {
			t.Fatalf("expected a compliant sign-in, got %v: %v", err, report.Violations)
		}
	})

	t.Run("ADR-0015: detects a session opened before the membership check, unnamed refusals and undocumented config", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, root, adr0015Files(
			adr0015Login("\t_ = uc.users.Upsert(ctx)\n\t_ = uc.sessions.Create(ctx)\n\treturn uc.requireAllowedOrgMember(ctx)"),
			"\tredirectWithAuthError(w, r, \"not_org_member\")\n\tredirectWithAuthError(w, r, \"org_membership_unverified\")\n",
			"  'not_org_member',\n",
			`{"auth.error.not_org_member": "Não"}`,
			`{}`,
			"nothing about the allowlist\n",
		))
		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0015 violations")
		}
		joined := strings.Join(report.Violations, "\n")
		for _, want := range []string{
			"ADR-0015: CompleteLogin in backend/internal/usecase/auth/auth.go calls users.Upsert before requireAllowedOrgMember",
			"ADR-0015: CompleteLogin in backend/internal/usecase/auth/auth.go calls sessions.Create before requireAllowedOrgMember",
			`ADR-0015: the sign-in callback sends auth_error "org_membership_unverified" but KNOWN_AUTH_ERRORS`,
			`ADR-0015: auth_error "not_org_member" has no "auth.error.not_org_member" in en-US.json`,
			"ADR-0015: docs/production-setup.md does not document ALLOWED_GITHUB_ORGS",
			"ADR-0015: .env.example does not document ALLOWED_GITHUB_ORGS",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("expected violation %q, got:\n%s", want, joined)
			}
		}
		if strings.Contains(joined, `"auth.error.not_org_member" in pt-BR.json`) {
			t.Errorf("pt-BR has the key and must not be reported:\n%s", joined)
		}
	})

	t.Run("ADR-0015: detects a sign-in that never checks membership", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, root, map[string]string{
			"backend/internal/usecase/auth/auth.go":      adr0015Login("\treturn uc.sessions.Create(ctx)"),
			"backend/internal/usecase/auth/auth_test.go": "package auth\n",
		})
		report, _ := adrcheck.ValidateRepository(root)
		if !containsViolation(report.Violations, "ADR-0015: CompleteLogin in backend/internal/usecase/auth/auth.go never calls requireAllowedOrgMember") {
			t.Fatalf("expected the missing check, got %v", report.Violations)
		}
	})

	adr0016Migrator := func(body string) map[string]string {
		return map[string]string{
			"backend/internal/infrastructure/database/postgres.go": `package database

import (
	"context"
	"database/sql"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
` + body + `
}
`,
			"backend/internal/infrastructure/database/postgres_test.go": "package database\n",
		}
	}

	t.Run("ADR-0016: migrations through a provider holding the session lock pass", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, root, adr0016Migrator(`	locker, _ := lock.NewPostgresSessionLocker()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err`))
		report, err := adrcheck.ValidateRepository(root)
		if err != nil || containsViolation(report.Violations, "ADR-0016") {
			t.Fatalf("expected locked migrations to pass, got %v: %v", err, report.Violations)
		}
	})

	t.Run("ADR-0016: detects the global lock-free goose API", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, root, adr0016Migrator(`	goose.SetBaseFS(fsys)
	return goose.UpContext(ctx, db, "migrations")`))
		report, _ := adrcheck.ValidateRepository(root)
		if !containsViolation(report.Violations, "ADR-0016: backend/internal/infrastructure/database/postgres.go calls goose.UpContext") {
			t.Fatalf("expected the global goose API to be reported, got %v", report.Violations)
		}
	})

	t.Run("ADR-0016: detects a provider without the session lock", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, root, adr0016Migrator(`	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	_ = lock.NewPostgresSessionLocker
	return err`))
		report, _ := adrcheck.ValidateRepository(root)
		if !containsViolation(report.Violations, "ADR-0016: backend/internal/infrastructure/database/postgres.go creates a goose provider without goose.WithSessionLocker") {
			t.Fatalf("expected the missing lock to be reported, got %v", report.Violations)
		}
	})

	t.Run("ADR-0014: a compliant static landing page passes", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, filepath.Join(root, "landing"), map[string]string{
			"index.html": `<!doctype html><html><head><link rel="stylesheet" href="styles.css"><link rel="icon" href="favicon.svg">` +
				`<link rel="canonical" href="https://example.github.io/site/"><meta property="og:image" content="https://example.github.io/site/a.webp"></head>` +
				`<body><a href="https://github.com/muriIo/pr-follower">GitHub</a><img src="images/inbox.webp" alt=""></body></html>`,
			"styles.css": `@font-face{font-family:Inter;src:url(fonts/inter.woff2) format("woff2")}` + "\n" + `.x{background:url("images/a.webp")}`,
		})

		report, err := adrcheck.ValidateRepository(root)
		if err != nil || containsViolation(report.Violations, "ADR-0014") {
			t.Fatalf("expected a compliant landing page, got %v: %v", err, report.Violations)
		}
	})

	t.Run("ADR-0014: detects a build, scripts and third-party requests in the landing page", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, filepath.Join(root, "landing"), map[string]string{
			"index.html": `<!doctype html><html><head>` +
				`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">` +
				`<script src="app.js"></script></head>` +
				`<body><img src="https://example.com/a.png" alt=""><a href="https://github.com">ok</a></body></html>`,
			"styles.css":   `@import url("https://fonts.example/x.css");` + "\n" + `.x{background:url(https://example.com/b.png)}`,
			"package.json": `{}`,
			"app.js":       `console.log(1)`,
		})

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0014 violations, got nil")
		}
		joined := strings.Join(report.Violations, "\n")
		for _, want := range []string{
			"ADR-0014: landing/package.json",
			"ADR-0014: landing/app.js",
			"ADR-0014: landing/index.html has a <script>",
			"ADR-0014: landing/index.html loads https://fonts.googleapis.com/css2?family=Inter",
			"ADR-0014: landing/index.html loads https://example.com/a.png",
			"ADR-0014: landing/styles.css loads https://fonts.example/x.css",
			"ADR-0014: landing/styles.css loads https://example.com/b.png",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("expected violation %q, got:\n%s", want, joined)
			}
		}
		if strings.Contains(joined, "loads https://github.com") {
			t.Errorf("a plain link is not a request and must not be reported:\n%s", joined)
		}
	})

	t.Run("ADR-0014: detects a landing directory without index.html", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		writeFiles(t, filepath.Join(root, "landing"), map[string]string{"styles.css": "body{}"})

		report, _ := adrcheck.ValidateRepository(root)
		if !containsViolation(report.Violations, "ADR-0014: landing/index.html is missing") {
			t.Fatalf("expected a missing index.html violation, got: %v", report.Violations)
		}
	})

	t.Run("ADR-0011: detects non-idempotent writes, schema, retries and missing redelivery tests", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)
		backend := filepath.Join(root, "backend", "internal")
		files := map[string]string{
			"adapter/persistence/queries/queries.sql": "-- name: UpsertThing :exec\nINSERT INTO things (id) VALUES ($1)\nON CONFLICT (id) DO NOTHING;\n\n" +
				"-- name: CreateToken :exec\n-- idempotent: random 256-bit key, cannot collide\nINSERT INTO tokens (id) VALUES ($1);\n\n" +
				"-- name: CreateDuplicate :exec\nINSERT INTO events (id) VALUES ($1);\n",
			"infrastructure/database/migrations/00002_more.sql": "-- +goose Up\nCREATE TABLE things (id BIGINT);\nCREATE INDEX idx_things ON things (id);\n",
			"adapter/notify/notify.go": `package notify

import (
	"context"
	"net/http"

	"example/resilience"
)

func Send(ctx context.Context) error {
	_, err := resilience.Retry(ctx, resilience.BackoffConfig{}, func(c context.Context) (bool, error) {
		_, err := http.NewRequestWithContext(c, http.MethodPost, "https://x", nil)
		return true, err
	})
	return err
}
`,
			"adapter/notify/notify_test.go": "package notify\nimport \"testing\"\nfunc TestSend(t *testing.T) {}\n",
			"usecase/webhook/ingest.go": `package webhook

import "context"

type UseCase struct{}

func (uc *UseCase) IngestPushEvent(ctx context.Context) error { return nil }
func (uc *UseCase) IngestStarEvent(ctx context.Context) error { return nil }
`,
			"usecase/webhook/ingest_test.go": `package webhook

import (
	"context"
	"testing"
)

func TestIngestPushEvent_RedeliveryIsIdempotent(t *testing.T) {
	_ = (&UseCase{}).IngestPushEvent(context.Background())
}

func TestIngestStarEvent_Works(t *testing.T) {
	_ = (&UseCase{}).IngestStarEvent(context.Background())
}
`,
		}
		for rel, content := range files {
			path := filepath.Join(backend, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil {
			t.Fatal("expected ADR-0011 violations")
		}
		joined := strings.Join(report.Violations, "\n")
		for _, want := range []string{
			"ADR-0011: query CreateDuplicate",
			"ADR-0011: 00002_more.sql: CREATE TABLE",
			"ADR-0011: 00002_more.sql: CREATE INDEX",
			"ADR-0011: backend/internal/adapter/notify/notify.go retries an http.MethodPost",
			"ADR-0011: webhook ingest method IngestStarEvent",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("expected violation %q, got:\n%s", want, joined)
			}
		}
		for _, unwanted := range []string{"UpsertThing", "CreateToken", "IngestPushEvent"} {
			if strings.Contains(joined, unwanted) {
				t.Errorf("%s is compliant and must not be reported:\n%s", unwanted, joined)
			}
		}
	})

	t.Run("ADR-0010: detects identity taken from a client header", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		handlerDir := filepath.Join(root, "backend", "internal", "adapter", "http")
		if err := os.MkdirAll(handlerDir, 0o755); err != nil {
			t.Fatalf("mkdir handler: %v", err)
		}
		content := "package httpadapter\nconst userHeader = \"X-User-" + "ID\"\n"
		if err := os.WriteFile(filepath.Join(handlerDir, "handler.go"), []byte(content), 0o644); err != nil {
			t.Fatalf("write handler.go: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil || !containsViolation(report.Violations, "ADR-0010") {
			t.Fatalf("expected ADR-0010 header violation, got err=%v violations=%v", err, report.Violations)
		}
	})

	t.Run("ADR-0010: detects a password column in a migration", func(t *testing.T) {
		t.Parallel()
		root := createValidSyntheticRepo(t)

		migDir := filepath.Join(root, "backend", "internal", "infrastructure", "database", "migrations")
		migration := "-- +goose Up\nALTER TABLE users ADD COLUMN password_hash TEXT;\n"
		if err := os.WriteFile(filepath.Join(migDir, "00002_password.sql"), []byte(migration), 0o644); err != nil {
			t.Fatalf("write migration: %v", err)
		}

		report, err := adrcheck.ValidateRepository(root)
		if err == nil || !containsViolation(report.Violations, "ADR-0010") {
			t.Fatalf("expected ADR-0010 password violation, got err=%v violations=%v", err, report.Violations)
		}
	})
}

// writeFiles writes each relative path under dir, creating the folders it needs.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func containsViolation(violations []string, prefix string) bool {
	for _, v := range violations {
		if strings.Contains(v, prefix) {
			return true
		}
	}
	return false
}

func createValidSyntheticRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// 1. Create docs/adr with README.md and ADR-0001
	adrDir := filepath.Join(root, "docs", "adr")
	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		t.Fatalf("mkdir adr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adrDir, "README.md"), []byte("# ADRs\n- [0001-clean-arch.md](./0001-clean-arch.md)\n"), 0o644); err != nil {
		t.Fatalf("write adr readme: %v", err)
	}
	adrContent := `# ADR-0001: Clean Architecture
## Status
Accepted
## Context
Context text.
## Decision
Decision text.
## Consequences
Consequences text.
## Compliance & Automated Gate
Gate text.
`
	if err := os.WriteFile(filepath.Join(adrDir, "0001-clean-arch.md"), []byte(adrContent), 0o644); err != nil {
		t.Fatalf("write 0001: %v", err)
	}

	// 2. Create backend/go.mod and backend/sqlc.yaml
	backendDir := filepath.Join(root, "backend")
	if err := os.MkdirAll(backendDir, 0o755); err != nil {
		t.Fatalf("mkdir backend: %v", err)
	}
	goMod := `module github.com/muriIo/pr-follower/backend
go 1.27.3
require (
	github.com/go-chi/chi/v5 v5.2.5
	github.com/pressly/goose/v3 v3.27.0
	github.com/jackc/pgx/v5 v5.9.1
)
`
	if err := os.WriteFile(filepath.Join(backendDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "sqlc.yaml"), []byte("version: '2'\n"), 0o644); err != nil {
		t.Fatalf("write sqlc.yaml: %v", err)
	}

	// 3. Create migrations
	migDir := filepath.Join(backendDir, "internal", "infrastructure", "database", "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migDir, "00001_init.sql"), []byte("-- +goose Up\nCREATE TABLE IF NOT EXISTS users (id BIGSERIAL PRIMARY KEY);\n"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	// 4. Create domain package + test
	domDir := filepath.Join(backendDir, "internal", "domain", "entity")
	if err := os.MkdirAll(domDir, 0o755); err != nil {
		t.Fatalf("mkdir domain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domDir, "entity.go"), []byte("package entity\ntype User struct { ID uint64 }\n"), 0o644); err != nil {
		t.Fatalf("write entity.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domDir, "entity_test.go"), []byte("package entity_test\nimport \"testing\"\nfunc TestUser(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write entity_test.go: %v", err)
	}

	// 5. Create usecase package + test
	ucDir := filepath.Join(backendDir, "internal", "usecase", "triage")
	if err := os.MkdirAll(ucDir, 0o755); err != nil {
		t.Fatalf("mkdir usecase: %v", err)
	}
	ucCode := `package triage
import "context"
type UseCase struct{}
func NewUseCase() *UseCase { return &UseCase{} }
func (u *UseCase) Execute(ctx context.Context) error { return nil }
`
	if err := os.WriteFile(filepath.Join(ucDir, "triage.go"), []byte(ucCode), 0o644); err != nil {
		t.Fatalf("write triage.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ucDir, "triage_test.go"), []byte("package triage_test\nimport \"testing\"\nfunc TestTriage(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write triage_test.go: %v", err)
	}

	// 6. Create adapter package + test
	ghDir := filepath.Join(backendDir, "internal", "adapter", "github")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatalf("mkdir adapter: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "github.go"), []byte("package github\ntype Client struct{}\n"), 0o644); err != nil {
		t.Fatalf("write github.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "github_test.go"), []byte("package github_test\nimport \"testing\"\nfunc TestClient(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write github_test.go: %v", err)
	}

	return root
}
