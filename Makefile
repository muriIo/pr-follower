.PHONY: help db-up db-down backend-fmt backend-vet backend-migration-check backend-arch-check backend-test backend-coverage backend-check frontend-test frontend-lint frontend-check check

help:
	@echo "PR Follower — Quality Gate & Development Commands"
	@echo "  make db-up                    Start PostgreSQL container via Docker Compose"
	@echo "  make db-down                  Stop PostgreSQL container"
	@echo "  make backend-fmt              Check Go formatting (gofmt -s)"
	@echo "  make backend-vet              Run go vet static analysis"
	@echo "  make backend-migration-check  Verify goose migrations are strictly forward-only (NO +goose Down)"
	@echo "  make backend-arch-check       Verify Clean Architecture dependency boundaries"
	@echo "  make backend-test             Run Go tests with race detector & coverage"
	@echo "  make backend-check            Run all Backend Quality Gates"
	@echo "  make frontend-check           Run all Frontend Quality Gates"
	@echo "  make check                    Run full monorepo Quality Gates"

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

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

backend-check: backend-fmt backend-vet backend-migration-check backend-arch-check backend-test

frontend-lint:
	@if [ -d frontend ]; then cd frontend && npm run lint; fi

frontend-test:
	@if [ -d frontend ]; then cd frontend && npm test -- --watch=false; fi

frontend-check: frontend-lint frontend-test

check: backend-check frontend-check
