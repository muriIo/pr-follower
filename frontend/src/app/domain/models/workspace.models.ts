export type PerspectiveView = 'seats' | 'inbox' | 'radar' | 'timeline';
export type SeatFilterType = 'todos' | 'turn' | 'deps';
export type RiskVerdictTier = 'baixo' | 'medio' | 'alto';
export type CiState = 'ok' | 'run' | 'fail';
export type OwnersState = 'ok' | 'voce' | 'pend';
export type SseConnectionState = 'conectado' | 'reconectando' | 'desconectado';

export interface DiffHunkLine {
  readonly t: 'hdr' | 'ctx' | 'add' | 'del';
  readonly s: string;
}

export interface DependencyViaNode {
  readonly label: string;
  readonly info: string;
}

export interface BlastSnippet {
  readonly start: number;
  readonly hit: number;
  readonly lines: readonly string[];
}

export interface BlastFileItem {
  readonly file: string;
  readonly kind: 'prod' | 'test';
  readonly uses: number;
  readonly breaks?: boolean;
  readonly snippets: readonly BlastSnippet[];
}

export interface PullRequestItem {
  readonly id: number;
  readonly rank: number;
  readonly repo: string;
  readonly title: string;
  readonly seatTitle?: string;
  readonly radarTitle?: string;
  readonly radarMeta?: string;
  readonly author: string;
  readonly branch: string;
  readonly verdict: RiskVerdictTier;
  readonly ci: CiState;
  readonly ciText: string;
  readonly owners: OwnersState;
  readonly ownersText: string;
  readonly size: string;
  readonly turn: boolean;
  readonly ownFiles?: number;
  readonly ownPill?: string;
  readonly claim?: string;
  readonly approvedBy?: string;
  readonly muteBadge?: string;
  readonly muteTip?: string;
  readonly why: string;
  readonly file: string;
  readonly deltaAdd?: string;
  readonly deltaDel?: string;
  readonly focusRule?: string;
  readonly hunk: readonly DiffHunkLine[];
  readonly dep?: boolean;
  readonly pkg?: string;
  readonly from?: string;
  readonly to?: string;
  readonly depType?: 'dependencies' | 'devDependencies';
  readonly score?: number;
  readonly rvDetail?: string;
  readonly via?: readonly DependencyViaNode[];
  readonly blast?: {
    readonly file: string;
    readonly start: number;
    readonly hit: number;
    readonly lines: readonly string[];
  };
  readonly blasts?: readonly BlastFileItem[];
  readonly note?: string;
  readonly noteWarn?: boolean;
}

export interface RepoInfo {
  readonly name: string;
  readonly branch: string;
}

export interface TimelineEventItem {
  readonly key: number;
  readonly t: number;
  readonly kind: 'ok' | 'fail' | 'turn' | 'info';
  readonly text: string;
  readonly repo: string;
  readonly pr?: number;
  readonly fresh?: boolean;
}

export interface WorkspaceDataset {
  readonly repos: readonly RepoInfo[];
  readonly prs: readonly PullRequestItem[];
  readonly events: readonly TimelineEventItem[];
  readonly approvedMap?: Record<number, boolean>;
  readonly silencedMap?: Record<number, string>;
}

export function computeSemverBumpLabel(fromVersion: string, toVersion: string): 'Major' | 'Minor' | 'Patch' {
  const fromParts = fromVersion.split('.');
  const toParts = toVersion.split('.');
  if (fromParts[0] !== toParts[0]) {
    return 'Major';
  }
  if (fromParts[1] !== toParts[1]) {
    return 'Minor';
  }
  return 'Patch';
}

export function isActionableTurn(
  pr: PullRequestItem,
  silenced: Record<number, string>,
  approved: Record<number, boolean>,
): boolean {
  return Boolean(pr.turn && !silenced[pr.id] && !approved[pr.id]);
}

export function isSafeBatchCandidate(pr: PullRequestItem, ciOverride?: CiState): boolean {
  const effectiveCi = ciOverride ?? pr.ci;
  return Boolean(pr.dep && pr.verdict === 'baixo' && effectiveCi === 'ok');
}

export function createInitialRepos(): readonly RepoInfo[] {
  return [
    { name: 'arvore/hive-web', branch: 'main' },
    { name: 'arvore/checkout-api', branch: 'main' },
    { name: 'arvore/design-system', branch: 'main' },
    { name: 'arvore/auth-service', branch: 'main' },
  ];
}

export function createInitialTimelineEvents(nowMs: number = Date.now()): readonly TimelineEventItem[] {
  const m = (mins: number) => nowMs - mins * 60_000;
  return [
    { key: 1, t: m(1), kind: 'ok', text: 'CI passou em #142', repo: 'arvore/hive-web', pr: 142 },
    { key: 2, t: m(3), kind: 'turn', text: 'CODEOWNERS pediu sua revisão em #412', repo: 'arvore/checkout-api', pr: 412 },
    { key: 3, t: m(9), kind: 'fail', text: 'CI falhou em #231', repo: 'arvore/checkout-api', pr: 231 },
    { key: 4, t: m(24), kind: 'info', text: 'Dependabot abriu #145 para vitest', repo: 'arvore/hive-web', pr: 145 },
    { key: 5, t: m(41), kind: 'ok', text: '@bianca aprovou #139', repo: 'arvore/hive-web', pr: 139 },
    { key: 6, t: m(95), kind: 'info', text: 'Ana Prado comentou em #412', repo: 'arvore/checkout-api', pr: 412 },
    { key: 7, t: m(190), kind: 'info', text: 'Bianca Reis abriu #298', repo: 'arvore/auth-service', pr: 298 },
  ];
}

export function createInitialPrDataset(): readonly PullRequestItem[] {
  const hdr = (s: string): DiffHunkLine => ({ t: 'hdr', s });
  const ctx = (s: string): DiffHunkLine => ({ t: 'ctx', s: '  ' + s });
  const add = (s: string): DiffHunkLine => ({ t: 'add', s: '+ ' + s });
  const del = (s: string): DiffHunkLine => ({ t: 'del', s: '- ' + s });

  const human: PullRequestItem[] = [
    {
      id: 142,
      rank: 1,
      repo: 'arvore/hive-web',
      title: 'feat(seats): add real-time CI status pulse to repo tiles',
      seatTitle: 'feat(seats): add real-time CI status pulse',
      author: '@bianca-dev',
      branch: 'feat/seat-ci-pulse',
      verdict: 'baixo',
      ci: 'ok',
      ciText: '12 de 12 checks passaram',
      owners: 'voce',
      ownersText: 'Você é dono de 1 arquivo',
      size: '14 arquivos',
      turn: true,
      ownFiles: 1,
      ownPill: '🎯 Toca apenas 1 arquivo seu (de 14 no PR)',
      claim: 'Assumido por @murilo',
      why: 'Mudança pequena e visual, limitada ao componente do Seat e coberta por testes.',
      file: 'src/app/features/seats-grid/seat-card.component.ts',
      deltaAdd: '+24',
      deltaDel: '-3',
      focusRule: '/src/app/ (@murilo, @arvore/frontend)',
      hunk: [
        hdr('@@ -18,6 +18,27 @@ export class SeatCardComponent {'),
        ctx('readonly repo = input.required<Repo>();'),
        ctx('private readonly ci = inject(CiStatusStore);'),
        ctx(''),
        del('readonly status = computed(() => this.ci.get(this.repo().name));'),
        del('readonly tone = computed(() => toneOf(this.status()));'),
        del('readonly label = computed(() => labelOf(this.status()));'),
        add('readonly status = toSignal('),
        add('  this.ci.watch(this.repo().name),'),
        add('  { initialValue: UNKNOWN_STATUS },'),
        add(');'),
        add(''),
        add('readonly tone = computed(() => toneOf(this.status()));'),
        add('readonly label = computed(() => labelOf(this.status()));'),
        add('readonly pulsing = signal(false);'),
        add(''),
        add('private pulseTimer?: number;'),
        add(''),
        add('constructor() {'),
        add('  effect(() => {'),
        add('    if (this.status().changedAt) {'),
        add('      this.startPulse();'),
        add('    }'),
        add('  });'),
        add('}'),
        add(''),
        add('private startPulse(): void {'),
        add('  this.pulsing.set(true);'),
        add('  clearTimeout(this.pulseTimer);'),
        add('  this.pulseTimer = window.setTimeout(() => this.pulsing.set(false), PULSE_MS);'),
        add('}'),
      ],
    },
    {
      id: 139,
      rank: 3,
      repo: 'arvore/hive-web',
      title: 'refactor(auth): token refresh',
      author: 'tiago-melo',
      branch: 'refactor/token-refresh',
      verdict: 'medio',
      ci: 'ok',
      ciText: '12 de 12 checks passaram',
      owners: 'ok',
      ownersText: 'Aprovado por @bianca',
      size: '4 arquivos, +88 −61',
      turn: false,
      approvedBy: '@bianca',
      why: 'Reorganiza a renovação de token sem alterar o contrato da API. Já aprovado pelo time.',
      file: 'src/auth/refresh.ts',
      hunk: [
        hdr('@@ -9,3 +9,3 @@ export async function refresh() {'),
        del('const res = await post("/token", old);'),
        add('const res = await tokenClient.renew(old);'),
        ctx('return store(res);'),
      ],
    },
    {
      id: 412,
      rank: 1,
      repo: 'arvore/checkout-api',
      title: 'fix(pix): idempotency key on retries',
      author: '@ana-prado',
      branch: 'fix/pix-idempotency',
      verdict: 'medio',
      ci: 'ok',
      ciText: '14 de 14 checks passaram',
      owners: 'voce',
      ownersText: 'Você é dono de 2 arquivos',
      size: '6 arquivos, +118 −37',
      turn: true,
      ownFiles: 2,
      ownPill: '🎯 Toca 2 arquivos seus (de 6 no PR)',
      deltaAdd: '+2',
      deltaDel: '-1',
      focusRule: '/src/payments/ (@murilo, @arvore/backend)',
      why: 'Boa cobertura de testes, mas altera o caminho de pagamento em produção.',
      file: 'src/payments/pix.ts',
      hunk: [
        hdr('@@ -58,3 +58,4 @@ async function charge(order) {'),
        del('return gateway.pix(order);'),
        add('const key = idempotencyKey(order.id);'),
        add('return gateway.pix(order, { key });'),
      ],
    },
    {
      id: 408,
      rank: 3,
      repo: 'arvore/checkout-api',
      title: 'feat(cart): persist applied coupons',
      author: 'diego-ramos',
      branch: 'feat/persist-coupons',
      verdict: 'baixo',
      ci: 'ok',
      ciText: '14 de 14 checks passaram',
      owners: 'ok',
      ownersText: 'Aprovado por @rafael',
      size: '2 arquivos, +31 −4',
      turn: false,
      approvedBy: '@rafael',
      why: 'Mudança aditiva e isolada no carrinho. Já aprovada pelo time.',
      file: 'src/cart/coupons.ts',
      hunk: [hdr('@@ -5,2 +5,3 @@'), ctx('cart.coupons.push(coupon);'), add('await repo.save(cart);')],
    },
    {
      id: 57,
      rank: 1,
      repo: 'arvore/design-system',
      title: 'feat(tokens): add drawer elevation',
      author: 'carla-nunes',
      branch: 'feat/elevation-drawer',
      verdict: 'baixo',
      ci: 'ok',
      ciText: '6 de 6 checks passaram',
      owners: 'ok',
      ownersText: 'Sem dono obrigatório',
      size: '2 arquivos, +9 −0',
      turn: false,
      why: 'Adiciona um token novo sem alterar os existentes.',
      file: 'tokens/elevation.json',
      hunk: [
        hdr('@@ -8,3 +8,6 @@'),
        ctx('"modal": { "shadow": "0 24px 48px #0008" },'),
        add('"drawer": {'),
        add('  "shadow": "-16px 0 40px #0006"'),
        add('},'),
      ],
    },
    {
      id: 301,
      rank: 1,
      repo: 'arvore/auth-service',
      title: 'refactor(session): rotate refresh tokens',
      author: 'tiago-melo',
      branch: 'refactor/rotate-refresh',
      verdict: 'alto',
      ci: 'ok',
      ciText: '10 de 10 checks passaram',
      owners: 'pend',
      ownersText: 'Aguardando @arvore/seguranca',
      size: '38 arquivos, +1.204 −871',
      turn: false,
      why: 'Altera a autenticação de todas as rotas e invalida as sessões ativas no deploy.',
      file: 'src/session/rotate.ts',
      hunk: [
        hdr('@@ -12,3 +12,3 @@ export async function rotate(sid) {'),
        del('return sessions.touch(sid);'),
        add('return sessions.reissue(sid, { family: true });'),
        ctx('}'),
      ],
    },
    {
      id: 298,
      rank: 2,
      repo: 'arvore/auth-service',
      title: 'feat(mfa): add recovery codes',
      author: 'bianca-reis',
      branch: 'feat/mfa-recovery',
      verdict: 'medio',
      ci: 'ok',
      ciText: '10 de 10 checks passaram',
      owners: 'pend',
      ownersText: 'Aguardando @arvore/seguranca',
      size: '7 arquivos, +212 −3',
      turn: false,
      why: 'Recurso novo atrás de feature flag, com uma migração aditiva.',
      file: 'src/mfa/recovery.ts',
      hunk: [
        hdr('@@ -0,0 +1,2 @@'),
        add('export const CODES = 10;'),
        add('export const generate = () => randomCodes(CODES);'),
      ],
    },
    {
      id: 138,
      rank: 9,
      repo: 'arvore/hive-mobile',
      title: 'fix(sse): debounce reconnect on network change',
      author: '@tiago-melo',
      branch: 'fix/sse-debounce',
      verdict: 'baixo',
      ci: 'ok',
      ciText: '8 de 8 checks passaram',
      owners: 'ok',
      ownersText: 'Você comentou no SHA atual',
      size: '2 arquivos, +17 −5',
      turn: false,
      muteBadge: '⏳ Aguardando Autor (você comentou no SHA atual)',
      muteTip: 'Volta para sua fila quando o autor enviar um novo commit.',
      why: 'Ajuste pequeno no cliente SSE. Volta para sua fila quando o autor enviar um novo commit.',
      file: 'src/sse/client.ts',
      hunk: [
        hdr('@@ -21,2 +21,2 @@'),
        del('onOnline(() => connect());'),
        add('onOnline(debounce(connect, 500));'),
      ],
    },
    {
      id: 136,
      rank: 9,
      repo: 'arvore/hive-admin',
      title: 'chore(ci): cache pnpm store across jobs',
      approvedBy: '@carlos',
      author: '@ana-prado',
      branch: 'chore/ci-pnpm-cache',
      verdict: 'baixo',
      ci: 'ok',
      ciText: '9 de 9 checks passaram',
      owners: 'ok',
      ownersText: 'Aprovado por @carlos',
      size: '1 arquivo, +6 −1',
      turn: false,
      muteBadge: '✅ Time já aprovou (@carlos de @arvore/frontend)',
      muteTip: 'A aprovação de um colega do seu time dispensa a sua revisão.',
      why: 'Mudança restrita ao pipeline. Sua revisão foi dispensada pela aprovação do time.',
      file: '.github/workflows/ci.yml',
      hunk: [
        hdr('@@ -14,1 +14,2 @@'),
        del('run: pnpm install'),
        add('cache: pnpm'),
        add('run: pnpm install --frozen-lockfile'),
      ],
    },
    {
      id: 144,
      rank: 9,
      repo: 'arvore/hive-admin',
      title: 'feat(radar): group alerts by ecosystem',
      author: '@carla-nunes',
      branch: 'feat/radar-groups',
      verdict: 'medio',
      ci: 'run',
      ciText: 'CI rodando, 3 de 9 checks',
      owners: 'pend',
      ownersText: 'Rascunho, revisão ainda não solicitada',
      size: '5 arquivos, +96 −20',
      turn: false,
      muteBadge: '🚧 Oculto pelo Gate (Rascunho / CI Pendente)',
      muteTip: 'Entra na sua fila quando sair do rascunho e o CI passar.',
      why: 'Ainda em rascunho. Entra na sua fila quando sair do rascunho e o CI passar.',
      file: 'src/radar/group.ts',
      hunk: [
        hdr('@@ -3,1 +3,1 @@'),
        del('return alerts;'),
        add('return groupBy(alerts, (a) => a.ecosystem);'),
      ],
    },
  ];

  const whyDep: Record<RiskVerdictTier, string> = {
    baixo: 'Atualização em devDependencies com CI verde. O pacote não chega ao bundle de produção.',
    medio: 'Minor em dependência de runtime. O changelog não lista breaking changes, mas o código roda em produção.',
    alto: 'Major com breaking changes no changelog. Exige ajuste de código antes de mesclar.',
  };

  const makeDep = (
    id: number,
    rank: number,
    repo: string,
    pkg: string,
    from: string,
    to: string,
    depType: 'dependencies' | 'devDependencies',
    verdict: RiskVerdictTier,
    extra: Partial<PullRequestItem> = {},
  ): PullRequestItem => ({
    id,
    rank,
    repo,
    pkg,
    from,
    to,
    depType,
    verdict,
    dep: true,
    title: `bump ${pkg} from ${from} to ${to}`,
    radarTitle: `${pkg} (${from} -> ${to})`,
    author: 'dependabot[bot]',
    branch: `dependabot/npm_and_yarn/${pkg}-${to}`,
    ci: 'ok',
    ciText: 'Todos os checks passaram',
    note: 'Nenhuma breaking change ou CVE registrada nesta versão.',
    via: [],
    owners: 'ok',
    ownersText: 'Sem dono obrigatório',
    size: '2 arquivos, lockfile incluído',
    turn: false,
    why: whyDep[verdict],
    file: 'package.json',
    hunk: [
      hdr(`@@ -18,3 +18,3 @@ "${depType}": {`),
      del(`"${pkg}": "^${from}",`),
      add(`"${pkg}": "^${to}",`),
    ],
    ...extra,
  });

  return [
    ...human,
    makeDep(89, 2, 'arvore/checkout-api', 'axios', '1.7.7', '2.0.0', 'dependencies', 'alto', {
      radarTitle: 'bump axios from 1.7.7 to 2.0.0',
      radarMeta: 'Major • Prod Transitiva via @nestjs/axios • Importado em 1 arquivo',
      score: 88,
      rvDetail: 'Atualização MAJOR em dependência transitiva de Produção (via @nestjs/axios) com 1 import ativo.',
      via: [{ label: '⚙️ @nestjs/axios v3.1.0 (Prod)', info: 'Dependência de produção que traz o axios para o projeto.' }],
      blast: {
        file: 'src/infra/payment-gateway.client.ts:4',
        start: 2,
        hit: 4,
        lines: [
          "import { Inject, Injectable } from '@nestjs/common';",
          "import { ConfigService } from '@nestjs/config';",
          "import axios, { AxiosInstance } from 'axios';",
          "import { PaymentRequest } from './payment.types';",
          'export const GATEWAY_TIMEOUT_MS = 8000;',
        ],
      },
      note: '⚠️ Breaking Change: v2.0.0 remove export default legado em CommonJS.',
      noteWarn: true,
    }),
    makeDep(145, 2, 'arvore/hive-web', 'vitest', '2.1.1', '2.1.4', 'devDependencies', 'baixo', {
      radarMeta: 'Dev Direta • 0 imports em produção',
      score: 12,
      rvDetail: 'Dependência direta de dev, 0 imports em produção, CI passando.',
    }),
    makeDep(146, 4, 'arvore/hive-web', 'nanoid', '3.3.6', '3.3.8', 'devDependencies', 'baixo', {
      radarMeta: 'Transitiva • 0 imports no código',
      score: 18,
      rvDetail: 'Dependência transitiva de build/dev, 0 imports diretos no seu código, CI passando.',
      via: [{ label: '⚙️ postcss v8.4.47 (Dev)', info: 'Dependência de build que traz o nanoid para o projeto.' }],
      file: 'pnpm-lock.yaml',
      hunk: [hdr('@@ -412,2 +412,2 @@'), del('nanoid@3.3.6:'), add('nanoid@3.3.8:')],
    }),
    makeDep(147, 5, 'arvore/hive-web', 'prettier', '3.3.2', '3.3.3', 'devDependencies', 'baixo', {
      radarMeta: 'Dev Direta • 0 imports em produção',
      score: 9,
      rvDetail: 'Dependência direta de dev, 0 imports em produção, CI passando.',
    }),
    makeDep(88, 2, 'arvore/design-system', 'storybook', '8.3.2', '8.3.5', 'devDependencies', 'baixo', {
      radarMeta: 'Dev Direta • 0 imports em produção',
      score: 16,
      rvDetail: 'Dependência direta de dev, 0 imports em produção, CI passando.',
    }),
    makeDep(233, 3, 'arvore/checkout-api', 'fastify', '4.28.1', '4.29.0', 'dependencies', 'medio', {
      radarMeta: 'Minor • Prod Direta • Importado em 7 arquivos',
      score: 46,
      rvDetail:
        'Atualização minor em dependência direta de Produção com 7 arquivos afetados, 3 deles usando API obsoleta. CI passando.',
      note: '⚠️ Breaking Change na v5: a v4.29.0 já marca reply.redirect(code, url) como obsoleto.',
      noteWarn: true,
      blasts: [
        {
          file: 'src/http/redirect.controller.ts:8',
          kind: 'prod',
          breaks: true,
          uses: 3,
          snippets: [
            {
              start: 7,
              hit: 8,
              lines: ['async function toCheckout(req, reply) {', "  return reply.redirect(302, '/checkout');", '}'],
            },
            {
              start: 19,
              hit: 20,
              lines: ['if (expired) {', "  return reply.redirect(301, '/login');", '}'],
            },
            {
              start: 41,
              hit: 42,
              lines: ['const target = resolveTarget(req);', '  return reply.redirect(307, target);', '}'],
            },
          ],
        },
        {
          file: 'src/http/webhook.routes.ts:12',
          kind: 'prod',
          breaks: true,
          uses: 1,
          snippets: [
            {
              start: 11,
              hit: 12,
              lines: [
                "app.get('/webhook/retry', (req, reply) => {",
                "  reply.redirect(303, '/webhook/status');",
                '});',
              ],
            },
          ],
        },
        {
          file: 'src/http/checkout.routes.ts:21',
          kind: 'prod',
          breaks: true,
          uses: 1,
          snippets: [
            {
              start: 20,
              hit: 21,
              lines: ['if (!cart.items.length) {', "  return reply.redirect(302, '/cart');", '}'],
            },
          ],
        },
        {
          file: 'src/main.ts:1',
          kind: 'prod',
          uses: 1,
          snippets: [
            {
              start: 1,
              hit: 1,
              lines: [
                "import fastify from 'fastify';",
                "import { registerRoutes } from './routes';",
                '',
                'const app = fastify({ logger: true });',
                'registerRoutes(app);',
              ],
            },
          ],
        },
        {
          file: 'src/http/plugins/cors.plugin.ts:2',
          kind: 'prod',
          uses: 1,
          snippets: [
            {
              start: 1,
              hit: 2,
              lines: [
                "import cors from '@fastify/cors';",
                "import { FastifyInstance } from 'fastify';",
                '',
                'export const corsPlugin = (app: FastifyInstance) => app.register(cors);',
              ],
            },
          ],
        },
        {
          file: 'test/http/webhook.routes.spec.ts:1',
          kind: 'test',
          uses: 1,
          snippets: [
            {
              start: 1,
              hit: 1,
              lines: ["import fastify from 'fastify';", "import { webhookRoutes } from '../../src/http/webhook.routes';"],
            },
          ],
        },
        {
          file: 'test/http/app.spec.ts:3',
          kind: 'test',
          uses: 1,
          snippets: [
            {
              start: 2,
              hit: 3,
              lines: ["import { describe, it } from 'vitest';", "import fastify from 'fastify';"],
            },
          ],
        },
      ],
    }),
    makeDep(231, 4, 'arvore/checkout-api', 'pg', '8.12.0', '8.13.0', 'dependencies', 'medio', {
      ci: 'fail',
      ciText: 'Falhou em testes de integração',
      radarMeta: 'Minor • Prod Direta • Importado em 1 arquivo',
      score: 58,
      rvDetail: 'Atualização minor em dependência direta de Produção com 1 import ativo e CI falhando.',
      blast: {
        file: 'src/infra/db.ts:1',
        start: 1,
        hit: 1,
        lines: ["import { Pool } from 'pg';", '', 'export const pool = new Pool({', '  connectionString: process.env.DATABASE_URL,', '});'],
      },
    }),
  ];
}
