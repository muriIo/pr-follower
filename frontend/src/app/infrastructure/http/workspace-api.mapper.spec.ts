import { describe, expect, it } from 'vitest';
import {
  ApiWorkspaceResponseDto,
  mapActivityEventToTimelineItem,
  mapApiWorkspaceResponseToDataset,
} from './workspace-api.mapper';

describe('workspace-api.mapper', () => {
  it('returns null when snapshot or seats are empty', () => {
    expect(mapApiWorkspaceResponseToDataset({})).toBeNull();
    expect(mapApiWorkspaceResponseToDataset({ snapshot: { seats: [] } })).toBeNull();
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
    expect(codePr.deltaAdd).toBe('+24');
    expect(codePr.deltaDel).toBe('-3');
    expect(codePr.hunk.length).toBe(4);

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

  it('marks repository access events as failures or recoveries that refresh the workspace', () => {
    const event = (type: string) =>
      mapActivityEventToTimelineItem({ id: type, type, repository_name: 'acme/web', summary: type, occurred_at: '2026-11-10T12:00:00Z' });

    expect(event('repository.access_lost')).toMatchObject({ kind: 'fail', refreshWorkspace: true });
    expect(event('repository.access_suspended')).toMatchObject({ kind: 'fail', refreshWorkspace: true });
    expect(event('repository.access_restored')).toMatchObject({ kind: 'ok', refreshWorkspace: true });
    expect(event('pr.approved').refreshWorkspace).toBeUndefined();
  });
});
