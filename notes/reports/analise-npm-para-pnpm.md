# Troca de npm por pnpm no frontend: segurança, desempenho e durabilidade

> Data: 2026-11-13 · Escopo: `frontend/` (o painel Angular; não existe pasta chamada "backoffice" no repositório, então considerei o frontend como o backoffice) · Ambiente: WSL2, Node `v24.22.2`, npm `11.19.1`, pnpm `12.19.0`.

## Veredito

**Vale a pena trocar.** O ganho de segurança é real. Ele vem principalmente dos *defaults* do pnpm, e não de recursos que o npm não tenha. O ganho de desempenho na instalação é grande: 3,5× no cold e cerca de 16× com cache quente. Build e testes ficam iguais. O custo é baixo: cerca de 8 arquivos para alterar e uma pegadinha de sintaxe (`--`). O ponto mais fraco é a durabilidade, porque o pnpm troca de major com mais frequência e o `corepack` saiu do Node 25+. Os dois riscos podem ser controlados.

Se a troca não for possível agora, cerca de 70% do ganho de segurança dá para obter **sem trocar**, configurando o npm 11.19 (veja a seção 1.3).

---

## 1. Segurança

### 1.1 O que muda na prática

| Proteção | npm 11.19 (default) | npm 11.19 (se configurado) | pnpm 12 (default) |
|---|---|---|---|
| Scripts de instalação (`postinstall` etc.) de dependências | **Executam todos** | `allowScripts` no `package.json` + `strict-allow-scripts=true` | **Bloqueados.** Só rodam os listados em `allowBuilds`; com `strictDepBuilds` (on) o install **falha** se aparecer script não revisado |
| Espera mínima após publicação (`minimumReleaseAge`) | Desligado (`min-release-age = null`) | `min-release-age=<dias>` | **1440 min (1 dia)** |
| Dependências transitivas vindas de git ou tarball URL | Permitido (`allow-git=all`, `allow-remote=all`) | `allow-git=root`, `allow-remote=root` | **Bloqueado** (`blockExoticSubdeps: true`) |
| Rebaixamento do nível de confiança do publicador | Sem equivalente | Sem equivalente | `trustPolicy: no-downgrade` |
| Dependências "fantasma" (importar pacote não declarado) | Possível: `node_modules` tem **199** entradas hoisted | Sem equivalente | **Impossível**: a raiz de `node_modules` expõe só os **7** pacotes declarados (+ `.pnpm`, `.bin`) |
| Auditoria de vulnerabilidades | `npm audit`: 0 vulnerabilidades | n/a | `pnpm audit`: 0 vulnerabilidades |

### 1.2 Medido neste projeto

- O lockfile atual tem **5 pacotes com install script**: `esbuild`, `lmdb`, `msgpackr-extract`, `@parcel/watcher` e `fsevents` (este último só no macOS). Hoje, com npm, todos executam código arbitrário no `npm ci`, incluindo dentro do `docker build`.
- No primeiro `pnpm install`, a instalação **falhou de propósito** com `ERR_PNPM_IGNORED_BUILDS` listando os 4 pacotes acima. É o comportamento esperado do `strictDepBuilds`.
- Configurei **todos os quatro como `false`** (nenhum script roda) e mesmo assim `tsc --noEmit`, `ng build` e os **191 testes (43 arquivos)** passaram. Esses pacotes trazem binários pré-compilados (`optionalDependencies` / prebuilds), então o script não é necessário aqui. **Conclusão: o frontend pode ficar com zero install scripts.**
- O tsc e o build passaram com o layout estrito, então não há import fantasma hoje. Com pnpm, um import fantasma futuro quebra o build em vez de passar sem ninguém perceber.

### 1.3 Alternativa sem trocar (npm endurecido)

O npm 11.19 já tem boa parte disso, só que desligado por padrão:

```jsonc
// package.json
"allowScripts": { "esbuild": false, "lmdb": false, "msgpackr-extract": false, "@parcel/watcher": false }
```
```ini
# .npmrc
strict-allow-scripts=true
min-release-age=1
allow-git=root
allow-remote=root
```

Isso não cobre o `trustPolicy` nem o layout estrito de `node_modules`. Também depende de ninguém apagar o `.npmrc`. No pnpm, o mais seguro é o default.

---

## 2. Desempenho

Medido em tmpfs, com o mesmo lockfile (o `pnpm-lock.yaml` foi gerado por `pnpm import` a partir do `package-lock.json`, sem mudar nenhuma versão), duas rodadas cada:

| Cenário | npm (`npm ci`) | pnpm (`pnpm install --frozen-lockfile`) | Ganho |
|---|---|---|---|
| Cold (cache/store vazio, sem `node_modules`) | 6,3 s / 6,6 s | 1,9 s / 1,8 s | **~3,5×** |
| Cache quente, sem `node_modules` (caso Docker/CI) | 5,4 s / 5,5 s | 0,34 s / 0,35 s | **~16×** |
| Nada mudou, `node_modules` presente | 0,46 s (`npm install`)¹ | 0,22 s / 0,23 s | ~2× |
| `ng build` | 3,3 s | 3,4 s | igual |
| `ng test` (191 testes) | 7,0 s | 6,2 s | igual (ruído) |

¹ `npm ci` sempre apaga o `node_modules`, então o no-op dele custa o mesmo que o warm (5,4 s). É por isso que o `docker-compose.local.yml` hoje precisa do truque do `.lock-hash`.

**Disco:**
- `node_modules`: 309 MB (npm) contra 273 MB (pnpm, hardlinks para o store). A vantagem real aparece com vários projetos na mesma máquina: o store é compartilhado e cada versão de pacote fica uma vez só no disco.
- `package-lock.json`: 239 KB / 7.254 linhas. `pnpm-lock.yaml`: 137 KB / 4.079 linhas, com diffs de PR mais legíveis.

**Onde o ganho não aparece:** na imagem final de produção. O estágio `nginx` só recebe `dist/`, então ela fica idêntica. O ganho fica no estágio de build e no dev.

---

## 3. Durabilidade (manutenção a longo prazo)

**A favor**
- O formato de lockfile `lockfileVersion: '9.0'` está estável desde o pnpm 9.
- Dependabot, Renovate, Angular CLI e VS Code suportam pnpm nativamente. O VS Code detecta o gerenciador pelo lockfile, e o Angular CLI usa `cli.packageManager` no `angular.json`.
- O layout estrito protege contra regressões silenciosas: um import fantasma passa a quebrar o build.
- O npm teve uma major nova (`12.2.2`) recentemente. Ficar no npm também exige acompanhar breaking changes.

**Contra / riscos**
- **Cadência de majors.** O pnpm 11 e o 12 saíram em pouco tempo, e o 11 mudou defaults de segurança que quebram instalações (ex.: `strictDepBuilds`). Cada major exige ler o changelog. Mitigação: fixar a versão exata em `packageManager` e deixar o Dependabot propor o bump.
- **`corepack`.** O Node 24 (usado hoje no `node:24-alpine`) ainda traz o `corepack`. O Node 25+ não traz mais. No upgrade de Node, a instalação do pnpm na imagem precisa mudar (`npm i -g pnpm@<versão>` ou a imagem/standalone do pnpm).
- **Governança.** O npm é mantido pelo GitHub/Microsoft. O pnpm é open source mantido por um time menor e financiado por patrocínio. O risco de abandono é baixo dado o tamanho da adoção, mas existe.
- **O próprio produto.** O analisador `backend/internal/adapter/analyzer/nodejs` aceita `pnpm-lock.yaml` em `CanAnalyze`, mas só reconstrói o caminho de dependência para npm v2/v3 (`notes/plans/roadmap.md`, item **P2**). Isso só importa se o PR Follower monitorar o próprio repositório: os PRs de Dependabot do frontend teriam análise mais pobre. Hoje não há `.github/dependabot.yml` no repo, então não afeta nada ainda.
- **Peer deps.** O `pnpm peers check` aponta um conflito transitivo (`@emnapi/core` / `@emnapi/runtime` `2.0.0-alpha.6` contra o pedido de `@napi-rs/wasm-runtime`). O conflito já existe com npm e passa em silêncio. Não quebrou nada, mas o pnpm vai mostrar o aviso.

---

## 4. Plano de migração (o que muda no repositório)

**Pegadinha principal.** O pnpm repassa o `--` literalmente para o script. Testei os dois casos:
- `pnpm test -- --watch=false` → `Unknown option ""` (falha)
- `pnpm run build -- --configuration production` → mesma falha

O correto é `pnpm test --watch=false` e `pnpm run build --configuration production`.

| Arquivo | Mudança |
|---|---|
| `frontend/package.json` | `"packageManager": "pnpm@12.19.0"` |
| `frontend/package-lock.json` | remover; gerar `frontend/pnpm-lock.yaml` com `pnpm import` |
| `frontend/pnpm-workspace.yaml` (novo) | `allowBuilds` com os 4 pacotes em `false` (abaixo) |
| `frontend/Dockerfile` | `corepack enable` + `pnpm install --frozen-lockfile` + cache mount; build sem `--` |
| `Makefile` (linhas 83 e 86) | `pnpm run lint`, `pnpm test --watch=false` |
| `docker-compose.local.yml` (painel) | hash do `pnpm-lock.yaml`, `pnpm install`, `pnpm start --host ...`, store dentro do volume |
| `frontend/angular.json` | `"packageManager": "pnpm"` |
| `AGENTS.md`, `frontend/.vscode/tasks.json` / `launch.json` | `npm start` → `pnpm start`; tasks `npm:` continuam funcionando, mas conferir |

`frontend/pnpm-workspace.yaml`:
```yaml
allowBuilds:
  '@parcel/watcher': false
  esbuild: false
  lmdb: false
  msgpackr-extract: false
```

`frontend/Dockerfile` (estágio de build):
```dockerfile
FROM node:24-alpine AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN --mount=type=cache,id=pnpm,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY . .
RUN pnpm run build --configuration production \
    && ...
```

`docker-compose.local.yml` (serviço `panel`): o pnpm usa hardlinks, e hardlink não atravessa sistemas de arquivos. O store precisa ficar **no mesmo volume** do `node_modules`. Fora dele, o pnpm cai para cópia e perde o ganho:
```sh
corepack enable
hash=$(sha1sum pnpm-lock.yaml)
[ "$(cat node_modules/.lock-hash 2>/dev/null)" = "$hash" ] || {
  pnpm install --frozen-lockfile --store-dir /app/node_modules/.pnpm-store && echo "$hash" > node_modules/.lock-hash; }
pnpm start --host 0.0.0.0 --poll 1000
```
O check de hash continua valendo, mas fica opcional: o no-op do pnpm custa 0,2 s.

---

## 5. Dry-run no branch `chore/pnpm` (2026-11-13)

Executado com Docker disponível, sem commit:

- **`make check`**: exit 0 (backend inalterado; `pnpm run lint` e `pnpm test --watch=false`, 191 testes).
- **Imagem de produção**: build OK. Sem cache de camadas: 16,1 s (npm, `main`) contra 14,8 s (pnpm, store mount quente). O primeiro build frio levou 18,9 s, porque inclui o download do pnpm pelo `corepack`. As duas imagens têm **74,3 MB**. O container sobe e o nginx serve a SPA (HTTP 200).
- **`make dev` sobre o volume `node_modules` antigo (npm)**: o pnpm reinstalou por cima sem prompt (`confirmModulesPurge=false`) em 3,5 s, e o `ng serve` subiu (HTTP 200 em :4200). Num restart sem mudança, o install é no-op em **19 ms**, então o truque do `.lock-hash` foi removido.
- **`minimumReleaseAge` contra Dependabot**: o log mostra que até o `--frozen-lockfile` passa por `Verifying lockfile against supply-chain policies`. Então um PR do Dependabot com versão de menos de 24 h deve falhar no install até a versão completar 1 dia. Isso é inferido pelo log, não reproduzido.
- **`pnpm import`** exige que o `packageManager` já aponte para pnpm. Com `npm@...` ele recusa.
- **Voltar para `main`** não exige limpeza: o `npm ci` do compose apaga o `node_modules` do volume e reinstala.

## Recomendação

1. Fazer a troca num `chore/pnpm`, com `allowBuilds` todo em `false` e `packageManager` fixado.
2. Corrigir os `--` no Makefile e no Dockerfile. É o único ponto que quebra.
3. ~~Validar `make docker-build` e `make dev`~~: feito no dry-run (seção 5).
4. Opcional, depois: adicionar `.github/dependabot.yml` com `package-ecosystem: npm` (o Dependabot usa esse nome também para pnpm). Avaliar priorizar o item P2 do roadmap, para o PR Follower analisar o próprio frontend.
