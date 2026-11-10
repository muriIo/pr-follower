# ADR-0008: English-Only Code/Commits and Multi-Locale `pt-BR` Default i18n

## Status
Accepted (2026-11-09)

## Context
To maintain a clean, globally consistent engineering standard while delivering a native Brazilian Portuguese (`pt-BR`) user experience today—and supporting additional languages (`en-US`, `es-ES`) in the future—we must separate source code language from UI presentation language.

## Decision
1. **English-Only Engineering Artifacts:**
   - 100% of source code identifiers (packages, types, functions, variables), code comments, test names, ADRs, API JSON field names, and Git Conventional Commit messages **must** be written in **English**.
2. **Externalized Multi-Locale i18n with `pt-BR` Default:**
   - Visible UI text defaults to **Brazilian Portuguese (`pt-BR`)** loaded from `frontend/public/i18n/pt-BR.json` (with `en-US.json` alongside it for multi-language switching).
   - Hardcoding visible user-facing strings directly inside Angular component templates is forbidden; components resolve text through the Signal-backed `LocaleStore` / `TranslatePipe`.

## Consequences
- **Positive:** Adding a new language requires only adding a new JSON locale file without modifying component logic.
- **Positive:** Codebase and commit history remain uniform in English.
- **Negative:** Every new UI label requires adding a translation key to the locale dictionary files.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` checks that when `frontend/` is present, `frontend/public/i18n/pt-BR.json` and `frontend/public/i18n/en-US.json` both exist and contain valid JSON with matching top-level translation keys.
- It also scans every template under `frontend/src/app/` for literal keys passed to the `t` pipe (including both branches of a ternary) and fails if any key is missing from `pt-BR.json` or `en-US.json`, since a missing key renders as the raw key on screen.
