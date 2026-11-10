import { describe, expect, it } from 'vitest';
import {
  ApiWorkspaceResponseDto,
  mapActivityEventToTimelineItem,
  mapApiWorkspaceResponseToDataset,
} from './workspace-api.mapper';

describe('workspace-api.mapper', () => {
  it('returns null without a snapshot, and an empty workspace when nothing is followed', () => {
    expect(mapApiWorkspaceResponseToDataset({})).toBeNull();
    // An empty answer is real: the UI must show nothing, not keep stale or demo data.
    expect(mapApiWorkspaceResponseToDataset({ snapshot: { seats: [] } })).toEqual({
      repos: [],
      prs: [],
      events: [],
      approvedMap: {},
      silencedMap: {},
    });
    expect(
      mapApiWorkspaceResponseToDataset({ snapshot: { seats: null as never }, timeline: null as never })?.repos,
    ).toEqual([]);
  });

  it('maps backend GET /api/v1/workspace snapshot and timeline into WorkspaceDataset', () => {
    const dto: ApiWorkspaceResponseDto = {
      snapshot: {
        seats: [
          {
            repository: {
              id: 1,
              owner: 'arvore',
              name: 'hive-web',
              full_name: 'arvore/hive-web',
              default_branch: 'main',
              cached_codeowners_rules: [{ pattern: '/src/app/', owners: ['@murilo', '@arvore/frontend'] }],
            },
            open_pr_count: 2,
            waiting_on_me_count: 1,
            dependabot_pr_count: 1,
            failing_ci_count: 0,
            pull_requests: [],
          },
        ],
        inbox_waiting_on_me: [
          {
            pull_request: {
              id: 10,
              repository_id: 1,
              repository_name: 'arvore/hive-web',
              number: 142,
              title: 'feat(seats): add real-time CI status pulse to repo tiles',
              author_login: 'bianca-dev',
              html_url: 'https://github.com/arvore/hive-web/pull/142',
              is_dependabot: false,
              is_draft: false,
              state: 'OPEN',
              mergeable_state: 'CLEAN',
              ci_status: 'SUCCESS',
              head_sha: '7f8a9b1',
              base_ref: 'main',
              claimed_by_login: 'murilo',
            },
            user_state: {
              pull_request_id: 10,
              user_id: 1,
              turn_state: 'WAITING_ON_ME',
              team_already_approved: false,
              owned_files_changed: [
                {
                  path: 'src/app/features/seats-grid/seat-card.component.ts',
                  additions: 24,
                  deletions: 3,
                  patch: '@@ -12,2 +12,3 @@\n-oldLine();\n+newLine();\n contextLine();',
                },
              ],
            },
          },
        ],
        inbox_dimmed: [
          {
            pull_request: {
              id: 11,
              repository_id: 1,
              repository_name: 'arvore/hive-web',
              number: 136,
              title: 'chore(ci): cache pnpm store',
              author_login: 'ana-prado',
              html_url: 'https://github.com/arvore/hive-web/pull/136',
              is_dependabot: false,
              is_draft: false,
              state: 'OPEN',
              mergeable_state: 'CLEAN',
              ci_status: 'SUCCESS',
              head_sha: '1a2b3c4',
              base_ref: 'main',
            },
            user_state: {
              pull_request_id: 11,
              user_id: 1,
              turn_state: 'TEAMMATE_APPROVED',
              team_already_approved: true,
              approved_by_login: 'carlos',
            },
          },
        ],
        dependabot_prs: [
          {
            pull_request: {
              id: 12,
              repository_id: 1,
              repository_name: 'arvore/hive-web',
              number: 89,
              title: 'chore(deps): bump axios from 1.7.7 to 2.0.0',
              author_login: 'dependabot[bot]',
              html_url: 'https://github.com/arvore/hive-web/pull/89',
              is_dependabot: true,
              is_draft: false,
              state: 'OPEN',
              mergeable_state: 'CLEAN',
              ci_status: 'SUCCESS',
              head_sha: '3a4b5c6',
              base_ref: 'main',
            },
            analysis: {
              pull_request_id: 12,
              package_name: 'axios',
              from_version: '1.7.7',
              to_version: '2.0.0',
              semver_bump_type: 'MAJOR',
              is_direct_dep: false,
              dep_scope: 'PROD',
              risk_verdict: 'HIGH',
              risk_score: 88,
              verdict_summary_key: 'dependabot.verdict.high',
              dependency_path_graph: [
                { name: 'hive-web', version: 'root', scope: 'PROD' },
                { name: '@nestjs/axios', version: '3.1.0', scope: 'PROD' },
                { name: 'axios', version: '2.0.0', scope: 'PROD' },
              ],
              blast_radius_usages: [
                {
                  file_path: 'src/infra/payment-gateway.client.ts',
                  line_number: 4,
                  import_stmt: "import axios from 'axios';",
                  code_snippet: "import { Injectable } from '@nestjs/common';\nimport axios from 'axios';",
                },
              ],
              advisories_and_notes: [
                {
                  severity: 'HIGH',
                  summary: 'Major v2.0.0 drops CommonJS default export',
                  is_breaking_note: true,
                },
              ],
            },
          },
        ],
      },
      timeline: [
        {
          id: 'ev-1',
          type: 'pr.approved',
          repository_name: 'arvore/hive-web',
          pr_number: 142,
          actor_login: 'murilo',
          summary: 'Approved PR #142',
          occurred_at: '2026-11-09T18:00:00Z',
        },
      ],
    };

    const dataset = mapApiWorkspaceResponseToDataset(dto);
    expect(dataset).not.toBeNull();
    expect(dataset!.repos).toEqual([{ name: 'arvore/hive-web', branch: 'main', access: 'active' }]);
    expect(dataset!.prs.length).toBe(3);

    const codePr = dataset!.prs.find((p) => p.id === 142)!;
    expect(codePr.turn).toBe(true);
    expect(codePr.claim).toBe('Assumido por @murilo');
    expect(codePr.files[0].additions).toBe(24);
    expect(codePr.files[0].deletions).toBe(3);
    expect(codePr.files[0].hunk.length).toBe(4);

    const mutedPr = dataset!.prs.find((p) => p.id === 136)!;
    expect(mutedPr.turn).toBe(false);
    expect(mutedPr.approvedBy).toBe('@carlos');

    const depPr = dataset!.prs.find((p) => p.id === 89)!;
    expect(depPr.dep).toBe(true);
    expect(depPr.verdict).toBe('alto');
    expect(depPr.score).toBe(88);
    expect(depPr.via?.length).toBe(1);
    expect(depPr.blasts?.length).toBe(1);
    expect(depPr.noteWarn).toBe(true);

    expect(dataset!.events.length).toBe(1);
    expect(dataset!.events[0].kind).toBe('ok');
    expect(dataset!.events[0].pr).toBe(142);
  });

  it('maps SSE ActivityEvent payloads to TimelineEventItem', () => {
    const item = mapActivityEventToTimelineItem({
      id: 'sse-1',
      type: 'pr.claimed',
      repository_name: 'arvore/checkout-api',
      pr_number: 412,
      summary: '@murilo claimed PR #412',
      occurred_at: '2026-11-09T19:00:00Z',
    });
    expect(item.kind).toBe('turn');
    expect(item.repo).toBe('arvore/checkout-api');
    expect(item.pr).toBe(412);
    expect(item.text).toBe('@murilo claimed PR #412');
  });

  it('maps repository access from installation webhooks onto seats', () => {
    const seat = (id: number, fullName: string, access?: 'active' | 'removed' | 'suspended') => ({
      repository: { id, owner: 'acme', name: fullName.split('/')[1], full_name: fullName, default_branch: 'main', access },
      open_pr_count: 0,
      waiting_on_me_count: 0,
      dependabot_pr_count: 0,
      failing_ci_count: 0,
      pull_requests: [],
    });
    const dataset = mapApiWorkspaceResponseToDataset({
      snapshot: {
        seats: [seat(1, 'acme/web', 'removed'), seat(2, 'acme/api', 'suspended'), seat(3, 'acme/legacy')],
        inbox_waiting_on_me: [],
        inbox_dimmed: [],
        dependabot_prs: [],
      },
      timeline: [],
    });
    expect(dataset!.repos.map((r) => r.access)).toEqual(['removed', 'suspended', 'active']);
  });

  it('shows what GitHub reported: no CI, no CODEOWNERS and the merge state are real states', () => {
    const pr = (id: number, repo: string, ci: 'NONE' | 'PENDING', mergeable: 'BEHIND' | 'UNKNOWN') => ({
      id,
      repository_id: id,
      repository_name: repo,
      number: id,
      title: `PR ${id}`,
      author_login: 'ana',
      html_url: '',
      is_dependabot: false,
      is_draft: false,
      state: 'OPEN' as const,
      mergeable_state: mergeable,
      ci_status: ci,
      head_sha: 'abc',
      base_ref: 'main',
    });
    const userState = (id: number) => ({
      pull_request_id: id,
      user_id: 1,
      turn_state: 'WAITING_ON_AUTHOR' as const,
      team_already_approved: false,
    });
    const seat = (id: number, fullName: string, codeowners?: '' | 'present' | 'absent') => ({
      repository: {
        id,
        owner: 'acme',
        name: fullName.split('/')[1],
        full_name: fullName,
        default_branch: 'main',
        codeowners_status: codeowners,
      },
      open_pr_count: 1,
      waiting_on_me_count: 0,
      dependabot_pr_count: 0,
      failing_ci_count: 0,
      pull_requests: [],
    });

    const dataset = mapApiWorkspaceResponseToDataset({
      snapshot: {
        seats: [seat(1, 'acme/bare', 'absent'), seat(2, 'acme/owned', 'present'), seat(3, 'acme/new', '')],
        inbox_dimmed: [
          { pull_request: pr(11, 'acme/bare', 'NONE', 'BEHIND'), user_state: userState(11) },
          { pull_request: pr(12, 'acme/owned', 'PENDING', 'UNKNOWN'), user_state: userState(12) },
        ],
      },
    })!;

    expect(dataset.repos.map((r) => r.codeowners)).toEqual(['absent', 'present', undefined]);

    const bare = dataset.prs.find((p) => p.id === 11)!;
    expect(bare.ci).toBe('none'); // the repository never reported a check: not "running"
    expect(bare.owners).toBe('none'); // no CODEOWNERS file: nobody's review is pending
    expect(bare.merge).toBe('behind');

    const owned = dataset.prs.find((p) => p.id === 12)!;
    expect(owned.ci).toBe('run');
    expect(owned.owners).toBe('others');
    expect(owned.merge).toBe('unknown');
  });

  it('marks every server activity as a workspace change, with access losses as failures', () => {
    const event = (type: string) =>
      mapActivityEventToTimelineItem({ id: type, type, repository_name: 'acme/web', summary: type, occurred_at: '2026-11-10T12:00:00Z' });

    expect(event('repository.access_lost')).toMatchObject({ kind: 'fail', refreshWorkspace: true });
    expect(event('repository.access_suspended')).toMatchObject({ kind: 'fail', refreshWorkspace: true });
    expect(event('repository.access_restored')).toMatchObject({ kind: 'ok', refreshWorkspace: true });
    // A new PR or commit from GitHub must show up in Seats without a manual refresh.
    expect(event('pr.opened').refreshWorkspace).toBe(true);
    expect(event('pr.synchronize').refreshWorkspace).toBe(true);
  });

  describe('never invents PR state (ADR: the view shows what GitHub reported)', () => {
    const repo = (full: string, codeowners?: 'present' | 'absent') => ({
      repository: { id: 1, owner: full.split('/')[0], name: full.split('/')[1], full_name: full, default_branch: 'main', ...(codeowners ? { codeowners_status: codeowners } : {}) },
      open_pr_count: 1,
      waiting_on_me_count: 0,
      dependabot_pr_count: 0,
      failing_ci_count: 0,
      pull_requests: [],
    });
    const pr = (number: number, full: string, extra: Record<string, unknown> = {}) => ({
      id: number, repository_id: 1, repository_name: full, number, title: `PR ${number}`, author_login: 'murilo', html_url: '',
      is_dependabot: false, is_draft: false, state: 'OPEN', mergeable_state: 'UNKNOWN', ci_status: 'NONE', head_sha: 'h', base_ref: 'main', ...extra,
    });
    const state = (extra: Record<string, unknown> = {}) => ({ pull_request_id: 1, user_id: 1, turn_state: 'HIDDEN_GATE', team_already_approved: false, ...extra });
    const map = (seats: unknown[], dimmed: unknown[], waiting: unknown[] = [], deps: unknown[] = []) =>
      mapApiWorkspaceResponseToDataset({
        snapshot: { seats, inbox_dimmed: dimmed, inbox_waiting_on_me: waiting, dependabot_prs: deps } as never,
        timeline: [],
      })!.prs;

    it('gives human PRs no risk verdict, no size, no invented file and an honest reason', () => {
      const [item] = map([repo('acme/web', 'absent')], [{ pull_request: pr(11, 'acme/web'), user_state: state() }]);
      expect(item.verdict).toBeNull();
      expect(item.size).toBeUndefined();
      expect(item.files).toEqual([]);
      expect(item.owners).toBe('none');
      expect(item.why).not.toContain('Gate de Turno');
      expect(item.why).toContain('não tem CODEOWNERS');
    });

    it('distinguishes an unchecked CODEOWNERS from files owned by others', () => {
      const prs = map(
        [repo('acme/unchecked'), repo('acme/owned', 'present')],
        [
          { pull_request: pr(1, 'acme/unchecked'), user_state: state() },
          { pull_request: pr(2, 'acme/owned'), user_state: state() },
        ],
      );
      expect(prs.find((p) => p.id === 1)!.owners).toBe('unknown');
      const owned = prs.find((p) => p.id === 2)!;
      expect(owned.owners).toBe('others');
      expect(owned.ownersText).toBe('Os arquivos alterados têm outros donos');
    });

    it('keeps every changed file the user owns, each with its own diff', () => {
      const files = [
        { path: 'src/a.ts', additions: 3, deletions: 1, patch: '@@ -1 +1 @@\n-old\n+new' },
        { path: 'src/b.ts', additions: 1, deletions: 0 },
      ];
      const [item] = map(
        [repo('acme/owned', 'present')],
        [],
        [{ pull_request: pr(3, 'acme/owned'), user_state: state({ turn_state: 'WAITING_ON_ME', owned_files_changed: files }) }],
      );
      expect(item.owners).toBe('voce');
      expect(item.ownersText).toBe('Você é dono de 2 arquivo(s)');
      expect(item.files.map((f) => [f.path, f.additions, f.deletions])).toEqual([
        ['src/a.ts', 3, 1],
        ['src/b.ts', 1, 0],
      ]);
      expect(item.files[0].hunk.length).toBe(3);
      expect(item.files[1].hunk).toEqual([]); // no patch: no fake hunk header
    });

    it('reads a Dependabot PR without analysis from its title, with no verdict or invented findings', () => {
      const dep = (title: string) => ({ pull_request: pr(40, 'acme/web', { is_dependabot: true, author_login: 'dependabot[bot]', title }) });
      const [parsed] = map([repo('acme/web', 'absent')], [], [], [dep('chore(deps): bump axios from 1.7.7 to 2.0.0')]);
      expect(parsed.verdict).toBeNull();
      expect([parsed.pkg, parsed.from, parsed.to]).toEqual(['axios', '1.7.7', '2.0.0']);
      expect(parsed.depType).toBeUndefined();
      expect(parsed.score).toBeUndefined();
      expect(parsed.size).toBeUndefined();
      expect(parsed.files).toEqual([]);
      expect(parsed.note).toBe('');
      expect(parsed.why).toBe('Análise de impacto ainda não executada para este PR.');
      expect(parsed.owners).toBe('none');

      const [unparsed] = map([repo('acme/web')], [], [], [dep('Bump the npm group across 3 directories')]);
      expect([unparsed.pkg, unparsed.from, unparsed.to]).toEqual([undefined, undefined, undefined]);
      expect(unparsed.radarTitle).toBe('Bump the npm group across 3 directories');
      expect(unparsed.owners).toBe('unknown');
    });
  });
});
