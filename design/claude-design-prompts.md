# PR Follower — Claude Design Prompt Suite (PT-BR UI)

Use these prompts in **Claude Design** (or v0 / Figma AI / Stitch) to generate the visual reference and HTML/CSS specifications before we implement Phase 4 in Angular v22.

---

## 0. Master Context & Design System Prompt (Paste First)

```markdown
You are designing **PR Follower**, a clean, minimal, low-cognitive-load developer dashboard inspired by **Hive (github.com/arvoreeducacao/hive)** and **Linear**.

### Core UX Principles
1. **Zero Mental Overload:** High signal-to-noise ratio. Muted backgrounds, crisp 1px borders, generous whitespace, and clear visual hierarchy. Never clutter cards with raw logs or secondary metadata until inspected.
2. **Hive-Inspired Layout:**
   - **Left Sidebar (240px fixed):** Controls the active perspective (`Minha Vez (Inbox)`, `Radar Dependabot`, `Seats de Repositórios`, `Linha do Tempo Ao Vivo`), shows live badge counts, a connection status indicator (`SSE Conectado`), and a language switcher (`PT-BR` active).
   - **Main Canvas (Fluid):** Displays either the Hive-style tiled **Seats Grid** (one live card/tile per followed repository) or a focused triage list.
   - **Right Slide-Over Inspection Drawer (560px):** Opens when clicking any PR card so the user never loses their place in the main grid.
3. **Interactive Action Cancellation:** Any network/GitHub action button (e.g., `Aprovar & Mesclar`, `Mesclar Lote Seguro`, `Sincronizar`) shows an inline spinner + a clear **`Cancelar`** button while in flight (wired to `AbortSignal`).
4. **Language:** All visible UI copy MUST be in **Brazilian Portuguese (PT-BR)**, keeping technical GitHub terms (`PR`, `CODEOWNERS`, `Dependabot`, `CI`, `devDependencies`, `squash`) natural for Brazilian engineers.

### Visual & Color Tokens (Dark-First Developer Palette)
- **Canvas Background:** `#0B0D11`
- **Surface / Seat Card:** `#12151C` (hover `#171B24`)
- **Elevated Drawer / Modal:** `#161A23`
- **Border:** `#222734` (active/focused border `#3B82F6`)
- **Typography:**
  - UI Sans: `Inter` or `Geist` (`13px` body, `11px` uppercase tracking-wider section headers, `15px` card titles)
  - Code / Monospace: `JetBrains Mono` (`12px` for SHAs, package versions, and diff hunks)
- **Semantic Verdict & Status Tokens:**
  - **Low Risk / Safe (`Baixo Risco`):** Emerald `#10B981` (`bg-emerald-500/10 text-emerald-400 border-emerald-500/20`)
  - **Medium Risk (`Médio Risco`):** Amber `#F59E0B` (`bg-amber-500/10 text-amber-400 border-amber-500/20`)
  - **High Risk / Breaking (`Alto Risco`):** Rose `#F43F5E` (`bg-rose-500/10 text-rose-400 border-rose-500/20`)
  - **My Turn Accent (`Sua Vez`):** Indigo `#6366F1`
```

---

## Prompt 1: Hive-Style Repo "Seats" Grid (`Seats de Repositórios`)

```markdown
Design the **Repo "Seats" Grid** view (`Seats de Repositórios`) of PR Follower in Brazilian Portuguese (PT-BR).

### Layout Structure
1. **Left Sidebar:**
   - App header: `PR Follower` with a subtle live green dot `Ao vivo (SSE)`.
   - Perspectives:
     - `Minha Vez (CODEOWNERS)` — badge `3`
     - `Radar Dependabot` — badge `7`
     - `Seats de Repositórios` (Active) — badge `4`
     - `Linha do Tempo`
   - Bottom footer: User avatar `@murilo`, team pill `@arvore/frontend`, and locale badge `PT-BR`.
2. **Top Header Bar:**
   - Title: `Seats de Repositórios` with subtitle `Monitoramento em tempo real dos repositórios seguidos`.
   - Filter pills: `Todos (4)`, `Com PRs na Minha Vez (2)`, `Dependabot Pendente (3)`.
   - Action button: `+ Seguir Repositório`.
3. **Main Area — Hive-Style Tiled "Seats" Grid (2x2 responsive CSS Grid):**
   - Each followed repository is a self-contained **Seat Tile** (`arvore/hive-web`, `arvore/checkout-api`, `arvore/design-system`, `arvore/auth-service`).
   - **Inside each Seat Tile:**
     - **Seat Header:** Repo name (`arvore/hive-web`), default branch pill (`main`), and a live CI health indicator dot (`CI Estável` in emerald or `1 CI Falhando` in rose).
     - **KPI Strip (3 compact pills):** `2 Sua Vez` (indigo), `3 Dependabot` (emerald/amber), `5 PRs Abertos`.
     - **Mini PR Stream (up to 3 rows inside the seat):**
       - Row 1: `#142 feat(seats): add real-time CI status pulse` — badge `Sua Vez` + `1 arquivo seu`
       - Row 2: `#145 bump vitest from 2.1.1 to 2.1.4` — badge `🟢 Baixo Risco (Dev)` + quick action `Aprovar & Squash`
       - Row 3 (dimmed 50% opacity): `#139 refactor(auth): token refresh` — badge `Aprovado pelo Time (@bianca)`
```

---

## Prompt 2: Turn-Based CODEOWNERS Inbox & Owned-Files Diff Drawer (`Minha Vez`)

```markdown
Design the **Turn-Based CODEOWNERS Inbox (`Minha Vez`)** with the **Right Slide-Over Inspection Drawer** open on a PR, in Brazilian Portuguese (PT-BR).

### Main List (Left 60% of Canvas)
- **Header:** `Minha Vez (CODEOWNERS)` with a toggle switch: `Mostrar PRs silenciados / aguardando autor (4)`.
- **Active Queue Section (`Aguardando Sua Revisão — 2`):**
  - Selected Card `#142` in `arvore/hive-web`:
    - Title: `feat(seats): add real-time CI status pulse to repo tiles`
    - Author: `@bianca-dev` • `CI Verde` • `Sem conflitos`
    - CODEOWNERS Highlight Pill: `🎯 Toca apenas 1 arquivo seu (de 14 no PR)`
    - Assignee / Claim badge: `Assumido por @murilo`
- **Dimmed / Muted Section (`Silenciados pelo Gate ou Aprovados pelo Time — 3`, rendered at 55% opacity):**
  - Card `#138`: Badge `⏳ Aguardando Autor (você comentou no SHA atual)`
  - Card `#136`: Badge `✅ Time já aprovou (@carlos de @arvore/frontend)`
  - Card `#144`: Badge `🚧 Oculto pelo Gate (Rascunho / CI Pendente)`

### Slide-Over Inspection Drawer (Right 560px)
- **Drawer Header:** `arvore/hive-web #142` • Button `Abrir no GitHub ↗`
- **CODEOWNERS Focus Banner:**
  - `Você foi acionado via regra /src/app/ (@murilo, @arvore/frontend). Exibindo apenas o diff dos arquivos sob sua responsabilidade.`
- **Inline Owned-File Diff Preview Card:**
  - File header: `src/app/features/seats-grid/seat-card.component.ts` (`+24 -3`)
  - Clean syntax-highlighted unified diff snippet (`JetBrains Mono`) showing the exact lines changed.
- **Sticky Action Footer (with Cooperative Cancellation state demo):**
  - Primary Button (shown in active/in-flight state): `Aprovando PR...` with spinner + destructive ghost button **`Cancelar (Esc)`**.
  - Secondary Buttons: `Silenciar até próximo commit`, `Silenciar até CI passar`, `Passar para colega`.
```

---

## Prompt 3: Dependabot Radar, Visual Dependency Tree & Blast Radius (`Radar Dependabot`)

```markdown
Design the **Dependabot Radar (`Radar Dependabot`)** view with the **Dependency Tree & Blast Radius Inspection Drawer** open, in Brazilian Portuguese (PT-BR).

### Main Area (Grouped by Risk Verdict)
- **Top Batch Action Banner:**
  - `🟢 4 PRs de Baixo Risco prontos para mesclagem segura (devDependencies ou transitivos sem import direto e CI verde)`
  - Primary Button: `Aprovar & Mesclar Todos os 4` (with an active in-flight progress bar `Mesclando 2 de 4...` and a **`Cancelar Lote`** button).
- **Group 1: `🔴 Alto Risco — Requer Análise de Impacto (1)`**
  - Selected Card: `arvore/checkout-api #89` — `bump axios from 1.7.7 to 2.0.0` (`Major` • `Prod Direta` • `Importado em 1 arquivo`).
- **Group 2: `🟢 Baixo Risco — Seguros para Merge Rápido (4)`**
  - Cards for `vitest (2.1.1 -> 2.1.4)`, `nanoid (3.3.6 -> 3.3.8)` showing `Transitiva • 0 imports no código`.

### Slide-Over Inspection Drawer (Right 560px — Inspecting `axios 1.7.7 -> 2.0.0` or `nanoid`)
1. **1-Glance Risk Verdict Card (Top of Drawer):**
   - Large Traffic-Light Banner:
     - Variant A (Low Risk): `🟢 Baixo Risco (Score 18/100) — Dependência transitiva de build/dev, 0 imports diretos no seu código, CI passando.`
     - Variant B (High Risk - Selected): `🔴 Alto Risco (Score 88/100) — Atualização MAJOR em dependência direta de Produção com 1 import ativo.`
2. **Interactive Visual Dependency Path Graph (`Árvore de Dependência`):**
   - Clean horizontal/vertical node flowchart with connected pills (NOT raw JSON):
     `[📦 checkout-api (root)]` ──▶ `[⚙️ @nestjs/axios v3.1.0 (Prod)]` ──▶ `[🎯 axios 1.7.7 → 2.0.0 (Major Bump)]`
3. **Blast Radius Code Preview (`Raio de Impacto no Código — 1 arquivo afetado`):**
   - Collapsible code card for `src/infra/payment-gateway.client.ts:4`
   - Inline 5-line code snippet highlighting `import axios, { AxiosInstance } from 'axios';`
4. **Breaking Changes & CVE Digest (`Notas de Quebra & CVEs`):**
   - Compact alert card: `⚠️ Breaking Change: v2.0.0 remove export default legado em CommonJS.`
5. **Drawer Footer Actions:**
   - `Aprovar & Squash`, `@dependabot rebase`, `@dependabot recreate`, `Silenciar`.
```
