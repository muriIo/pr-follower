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
	if err := os.WriteFile(filepath.Join(migDir, "00001_init.sql"), []byte("-- +goose Up\nCREATE TABLE users (id BIGSERIAL PRIMARY KEY);\n"), 0o644); err != nil {
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
