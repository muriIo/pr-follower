# ADR-0009: Angular v22 Standalone, Signals, and Angular CLI MCP Architecture

## Status
Accepted (2026-11-09)

## Context
The frontend requires real-time reactive updates (SSE live repository "Seats" grid, Turn-Based CODEOWNERS Inbox, and Dependabot Radar with interactive dependency graphs) while maintaining strict Clean Architecture and AI-assisted workspace tooling.

## Decision
1. **Angular v22 Core Conventions:**
   - All components use **Standalone Components** (`standalone: true` default in v22), `ChangeDetectionStrategy.OnPush`, and modern control flow (`@if`, `@for`, `@switch`).
   - Reactive state management in `src/app/application/stores/` is built with **Angular Signals** (`signal`, `computed`, `effect`).
   - Dependency injection uses `inject()` and typed `InjectionToken` gateways bridging `domain` ports to `infrastructure` adapters.
2. **Angular CLI MCP Server (`ng mcp`):**
   - `frontend/.mcp.json` configures the official Angular CLI Model Context Protocol server (`npx -y @angular/cli mcp` — https://angular.dev/ai/mcp) to provide workspace metadata and Angular v22 best-practice verification.

## Consequences
- **Positive:** Fine-grained reactive updates without heavy RxJS subscription boilerplate in UI components.
- **Positive:** First-class AI agent awareness of the Angular v22 workspace via `ng mcp`.
- **Negative:** Requires strict adherence to `OnPush` and Signal immutability patterns.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` verifies that when `frontend/` is present, `frontend/.mcp.json` exists and configures `@angular/cli` MCP, and that `frontend/src/app` follows the `domain` / `application` / `infrastructure` / `presentation` Clean Architecture directory structure.
