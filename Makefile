.PHONY: help db-up db-down dev dev-down docker-build adr-check backend-fmt backend-vet backend-migration-check backend-arch-check backend-test backend-coverage backend-check frontend-test frontend-lint frontend-check check security security-secrets security-deps security-code security-workflows

help:
	@echo "PR Follower — Quality Gate & Development Commands"
	@echo "  make db-up                    Start PostgreSQL container via Docker Compose"
	@echo "  make db-down                  Stop PostgreSQL container"
	@echo "  make dev                      Run the full dev stack (postgres + api + panel) via docker-compose.local.yml"
	@echo "  make dev-down                 Stop the dev stack"
	@echo "  make docker-build             Build production images (pr-follower-api, pr-follower-panel)"
	@echo "  make adr-check                Validate all Architecture Decision Records (docs/adr/) against the codebase"
	@echo "  make backend-fmt              Check Go formatting (gofmt -s)"
	@echo "  make backend-vet              Run go vet static analysis"
	@echo "  make backend-migration-check  Verify goose migrations are strictly forward-only (NO +goose Down)"
	@echo "  make backend-arch-check       Verify Clean Architecture dependency boundaries"
	@echo "  make backend-test             Run Go tests with race detector & coverage"
	@echo "  make backend-check            Run all Backend Quality Gates (including ADR compliance)"
	@echo "  make frontend-check           Run all Frontend Quality Gates"
	@echo "  make check                    Run full monorepo Quality Gates"
	@echo "  make security                 Run the security scans (secrets, dependencies, code, workflows; needs Docker)"

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

dev:
	docker compose -f docker-compose.local.yml up -d

dev-down:
	docker compose -f docker-compose.local.yml down

docker-build:
	docker build -t pr-follower-api backend
	docker build -t pr-follower-panel frontend

adr-check:
	@echo "==> Validating Architecture Decision Records (docs/adr/) compliance..."
	@cd backend && go test -v -run TestValidateRepository_LiveMonorepo ./internal/infrastructure/adrcheck/...
	@echo "==> All ADRs verified."

backend-fmt:
	@echo "==> Checking Go formatting..."
	@cd backend && test -z "$$(gofmt -s -l .)" || (echo "Formatting issues found:" && gofmt -s -d . && exit 1)

backend-vet:
	@echo "==> Running go vet..."
	@cd backend && go vet ./...

backend-migration-check:
	@echo "==> Verifying goose migrations are strictly forward-only (no '+goose Down')..."
	@if grep -i -n "+goose Down" backend/internal/infrastructure/database/migrations/*.sql >/dev/null 2>&1; then \
		echo "VIOLATION: '+goose Down' found in migration files! Always add a new forward migration instead."; \
		grep -i -n "+goose Down" backend/internal/infrastructure/database/migrations/*.sql; \
		exit 1; \
	fi
	@echo "==> Forward-only migration gate verified."

backend-arch-check:
	@echo "==> Verifying Clean Architecture boundaries in backend..."
	@cd backend && \
	DOMAIN_IMPORTS=$$(go list -f '{{join .Imports "\n"}}' ./internal/domain/... 2>/dev/null | grep -E 'pr-follower/backend/internal/(usecase|adapter|infrastructure)|database/sql|pressly/goose|go-chi|net/http' || true); \
	if [ -n "$$DOMAIN_IMPORTS" ]; then \
		echo "VIOLATION: domain layer imports forbidden packages: $$DOMAIN_IMPORTS"; \
		exit 1; \
	fi; \
	USECASE_IMPORTS=$$(go list -f '{{join .Imports "\n"}}' ./internal/usecase/... 2>/dev/null | grep -E 'pr-follower/backend/internal/(adapter|infrastructure)|database/sql|pressly/goose|go-chi' || true); \
	if [ -n "$$USECASE_IMPORTS" ]; then \
		echo "VIOLATION: usecase layer imports forbidden outer packages: $$USECASE_IMPORTS"; \
		exit 1; \
	fi
	@echo "==> Clean Architecture boundaries verified."

backend-test:
	@echo "==> Running Go tests with race detector & coverage..."
	@cd backend && go test -v -race -coverprofile=coverage.out ./...

backend-coverage: backend-test
	@cd backend && go tool cover -func=coverage.out

backend-check: adr-check backend-fmt backend-vet backend-migration-check backend-arch-check backend-test

frontend-lint:
	@if [ -d frontend ]; then cd frontend && pnpm run lint; fi

frontend-test:
	@if [ -d frontend ]; then cd frontend && pnpm test --watch=false; fi

frontend-check: frontend-lint frontend-test

check: backend-check frontend-check

# Security scans (docs/security.md). Each scanner runs from a pinned image, so a local run and CI
# give the same answer. Every target fails on a finding.
GITLEAKS_IMAGE ?= zricethezav/gitleaks:v8.24.3
OSV_IMAGE      ?= ghcr.io/google/osv-scanner:v2.0.2
SEMGREP_IMAGE  ?= semgrep/semgrep:1.122.0
ZIZMOR_IMAGE   ?= ghcr.io/zizmorcore/zizmor:1.9.0

security: security-secrets security-deps security-code security-workflows

# Secrets in every commit of every branch, not only the current tree. Matches are redacted.
security-secrets:
	docker run --rm -v "$(CURDIR)":/repo:ro $(GITLEAKS_IMAGE) git /repo --log-opts="--all" --redact --no-banner

# Known vulnerabilities (OSV) in the Go modules and the frontend packages. pnpm writes its own
# environment as a first YAML document in pnpm-lock.yaml and OSV-Scanner reads only that one, so the
# app's document is handed to it alone. Accepted findings, each with an expiry, are in osv-scanner.toml.
security-deps:
	@tmp=$$(mktemp -d) && \
	awk 'n >= 2; /^---$$/ { n++ }' frontend/pnpm-lock.yaml > $$tmp/pnpm-lock.yaml && \
	if [ ! -s $$tmp/pnpm-lock.yaml ]; then cp frontend/pnpm-lock.yaml $$tmp/pnpm-lock.yaml; fi && \
	docker run --rm -v "$(CURDIR)":/src:ro -v $$tmp:/lock:ro $(OSV_IMAGE) scan source --no-call-analysis=go \
		--config /src/osv-scanner.toml \
		-L /src/backend/go.mod -L /lock/pnpm-lock.yaml; \
	status=$$?; rm -rf $$tmp; exit $$status

# Code patterns (injection, SSRF, unsafe crypto, Dockerfile mistakes). Rules come from the Semgrep
# registry; no code or metrics leave the machine.
security-code:
	docker run --rm -v "$(CURDIR)":/src:ro $(SEMGREP_IMAGE) semgrep scan --error --metrics=off \
		--config p/default --config p/golang --config p/typescript --config p/dockerfile /src

# GitHub Actions workflows (unpinned actions, leaked credentials, injection).
security-workflows:
	docker run --rm -v "$(CURDIR)":/src:ro $(ZIZMOR_IMAGE) --offline /src/.github/workflows
