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
          pull_request_id: 10,
          pr_number: 142,
          actor_login: 'murilo',
          occurred_at: '2026-11-09T18:00:00Z',
        },
      ],
    };

    const dataset = mapApiWorkspaceResponseToDataset(dto);
    expect(dataset).not.toBeNull();
    expect(dataset!.repos).toEqual([{ name: 'arvore/hive-web', branch: 'main', access: 'active' }]);
    expect(dataset!.prs.length).toBe(3);

    const codePr = dataset!.prs.find((p) => p.number === 142)!;
    expect(codePr.id).toBe(10);
    expect(codePr.turn).toBe(true);
    expect(codePr.claim).toEqual({ key: 'inbox.claim', params: { login: '@murilo' } });
    expect(codePr.files[0].additions).toBe(24);
    expect(codePr.files[0].deletions).toBe(3);
    expect(codePr.files[0].hunk.length).toBe(4);

    const mutedPr = dataset!.prs.find((p) => p.number === 136)!;
    expect(mutedPr.turn).toBe(false);
    expect(mutedPr.approvedBy).toBe('@carlos');

    const depPr = dataset!.prs.find((p) => p.number === 89)!;
    expect(depPr.dep).toBe(true);
    expect(depPr.verdict).toBe('alto');
    expect(depPr.score).toBe(88);
    expect(depPr.via?.length).toBe(1);
    expect(depPr.blasts?.length).toBe(1);
    expect(depPr.noteWarn).toBe(true);

    expect(dataset!.events.length).toBe(1);
    expect(dataset!.events[0].kind).toBe('ok');
    expect(dataset!.events[0].pr).toBe(10);
  });

  it('maps SSE ActivityEvent payloads to TimelineEventItem', () => {
    const item = mapActivityEventToTimelineItem({
      id: 'sse-1',
      type: 'pr.claimed',
      repository_name: 'arvore/checkout-api',
      pull_request_id: 55,
      pr_number: 412,
      actor_login: 'murilo',
      occurred_at: '2026-11-09T19:00:00Z',
    });
    expect(item.kind).toBe('turn');
    expect(item.repo).toBe('arvore/checkout-api');
    expect(item.pr).toBe(55);
    expect(item.text).toEqual({ key: 'timeline.event.pr.claimed', params: { repo: 'arvore/checkout-api', pr: 412, actor: '@murilo' } });
  });

  it('phrases a CODEOWNERS refresh, with its own sentence when the file was removed', () => {
    const event = (status: string, rules: string) =>
      mapActivityEventToTimelineItem({
        id: 'c', type: 'repository.codeowners_updated', repository_name: 'acme/web',
        params: { status, rules }, occurred_at: '2026-11-10T12:00:00Z',
      });

    expect(event('present', '3').text).toEqual({
      key: 'timeline.event.repository.codeowners_updated',
      params: { repo: 'acme/web', pr: '', actor: '', status: 'present', rules: '3' },
    });
    expect(event('absent', '0').text).toMatchObject({ key: 'timeline.event.repository.codeowners_updated.absent' });
    expect(event('present', '3')).toMatchObject({ refreshWorkspace: true, kind: 'info' });
  });

  it('phrases activity from its type and params, so the timeline follows the locale', () => {
    const opened = mapActivityEventToTimelineItem({
      id: 'e1',
      type: 'pr.opened',
      repository_name: 'acme/web',
      pr_number: 7,
      params: { title: 'feat: x' },
      occurred_at: '2026-11-10T12:00:00Z',
    });
    expect(opened.text).toEqual({ key: 'timeline.event.pr.opened', params: { repo: 'acme/web', pr: 7, actor: '', title: 'feat: x' } });

    const unknown = mapActivityEventToTimelineItem({ id: 'e2', type: 'pr.labeled', repository_name: 'acme/web', occurred_at: '2026-11-10T12:00:00Z' });
    const analysed = mapActivityEventToTimelineItem({
      id: 'e3',
      type: 'dependabot.analyzed',
      repository_name: 'acme/web',
      params: { package: 'axios', from: '1.0.0', to: '2.0.0', verdict: 'HIGH' },
      occurred_at: '2026-11-10T12:00:00Z',
    });
    expect(analysed.text).toMatchObject({ key: 'timeline.event.dependabot.analyzed.high' });

    expect(unknown.text).toEqual({ key: 'timeline.event.other', params: { repo: 'acme/web', pr: '', actor: '', type: 'pr.labeled' } });
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
      mapActivityEventToTimelineItem({ id: type, type, repository_name: 'acme/web', occurred_at: '2026-11-10T12:00:00Z' });

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
      expect(item.why).toEqual({ key: 'drawer.why.none' });
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
      expect(owned.ownersText).toEqual({ key: 'owners.others' });
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
      expect(item.ownersText).toEqual({ key: 'owners.voce.count.other', params: { count: 2 } });
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
      expect(parsed.why).toEqual({ key: 'drawer.why.depNone' });
      expect(parsed.owners).toBe('none');

      const [unparsed] = map([repo('acme/web')], [], [], [dep('Bump the npm group across 3 directories')]);
      expect([unparsed.pkg, unparsed.from, unparsed.to]).toEqual([undefined, undefined, undefined]);
      expect(unparsed.radarTitle).toBe('Bump the npm group across 3 directories');
      expect(unparsed.owners).toBe('unknown');
    });

    it('says an analysis failed, and why, instead of "not analysed"', () => {
      const failed = (id: number, kind: string) => ({
        pull_request: pr(id, 'acme/web', { is_dependabot: true, author_login: 'dependabot[bot]', title: 'chore(deps): bump axios from 1.7.7 to 2.0.0' }),
        analysis_failure: { error_kind: kind, message: 'GitHub said no', failed_at: '2026-11-16T20:00:00Z' },
      });
      const [forbidden, unknown, unreadable] = map([repo('acme/web')], [], [], [
        failed(41, 'forbidden'),
        failed(42, 'something_new'),
        failed(43, 'not_analysable'),
      ]);

      expect(forbidden.verdict).toBeNull();
      expect(forbidden.analysisFailed).toBe(true);
      expect(forbidden.analysisRetryable).toBe(true);
      expect(forbidden.radarMeta).toEqual({ key: 'radar.meta.failed' });
      expect(forbidden.rvDetail).toEqual({ key: 'impact.rvDetail.failed.forbidden' });
      expect(forbidden.why).toEqual({ key: 'drawer.why.depFailed' });
      expect(unknown.rvDetail).toEqual({ key: 'impact.rvDetail.failed.error' });
      // A lockfile no analyzer reads will fail again: say so, and offer no retry.
      expect(unreadable.rvDetail).toEqual({ key: 'impact.rvDetail.failed.not_analysable' });
      expect(unreadable.analysisRetryable).toBe(false);
    });
  });

  it('keeps the activity type, actor and id, and marks only live activity as new', () => {
    const ev = { id: 'ev-1', type: 'pr.merged', repository_name: 'acme/web', pr_number: 7, actor_login: 'octocat', occurred_at: '2026-11-10T12:00:00Z' };
    expect(mapActivityEventToTimelineItem(ev)).toMatchObject({ id: 'ev-1', type: 'pr.merged', actor: 'octocat', fresh: true });

    const loaded = mapApiWorkspaceResponseToDataset({ snapshot: { seats: [] }, timeline: [ev] })!.events[0];
    expect(loaded).toMatchObject({ id: 'ev-1', type: 'pr.merged', actor: 'octocat', fresh: false });
    expect(loaded.t).toBe(Date.parse('2026-11-10T12:00:00Z'));
  });

  describe('turn engine: my inbox holds only PRs that touch my files', () => {
    const pr = (number: number, extra: Record<string, unknown> = {}) => ({
      id: 1000 + number, repository_id: 1, repository_name: 'acme/web', number, title: `PR ${number}`, author_login: 'bia', html_url: '',
      is_dependabot: false, is_draft: false, state: 'OPEN', mergeable_state: 'CLEAN', ci_status: 'SUCCESS', head_sha: 'h', base_ref: 'main', ...extra,
    });
    const seat = (prs: unknown[], codeowners: 'present' | 'absent' = 'present') => ({
      repository: { id: 1, owner: 'acme', name: 'web', full_name: 'acme/web', default_branch: 'main', codeowners_status: codeowners },
      open_pr_count: prs.length, waiting_on_me_count: 0, dependabot_pr_count: 0, failing_ci_count: 0, pull_requests: prs,
    });
    const state = (turn: string, owned: number, extra: Record<string, unknown> = {}) => ({
      pull_request_id: 1, user_id: 1, turn_state: turn, team_already_approved: false,
      owned_files_changed: Array.from({ length: owned }, (_, i) => ({ path: `src/f${i}.ts`, additions: 1, deletions: 0, patch: '' })), ...extra,
    });
    const map = (snapshot: Record<string, unknown>) => mapApiWorkspaceResponseToDataset({ snapshot: snapshot as never, timeline: [] })!.prs;

    it('keeps a PR I do not own in its seat, outside the inbox, even when it is a draft', () => {
      const [item, ...rest] = map({ seats: [seat([pr(5, { is_draft: true })])] });
      expect(rest).toEqual([]);
      expect(item).toMatchObject({ id: 1005, number: 5, repo: 'acme/web', turn: false, owners: 'others' });
      expect(item.why).toEqual({ key: 'drawer.why.others' });
      // No badge: a badge is what lists a PR under "muted" in Minha Vez.
      expect(item.muteBadge).toBeUndefined();
      expect(item.ownPill).toBeUndefined();
    });

    it('shows a PR whose files are not known yet as not evaluated, never as my turn', () => {
      const [item] = map({ seats: [seat([pr(5)])], ownership_pending_pr_ids: [1005] });
      expect(item).toMatchObject({ id: 1005, number: 5, turn: false, owners: 'unknown' });
      expect(item.why).toEqual({ key: 'drawer.why.pending' });
      expect(item.muteBadge).toBeUndefined();
    });

    it('says nobody is assigned when the repository has no CODEOWNERS', () => {
      const [item] = map({ seats: [seat([pr(5)], 'absent')] });
      expect(item).toMatchObject({ turn: false, owners: 'none' });
    });

    it('lists each PR once: inbox and radar PRs are not repeated from the seat', () => {
      const mine = pr(1);
      const gated = pr(2, { ci_status: 'FAILURE' });
      const bump = pr(3, { is_dependabot: true, author_login: 'dependabot[bot]' });
      const other = pr(4);
      const prs = map({
        seats: [seat([mine, gated, bump, other])],
        inbox_waiting_on_me: [{ pull_request: mine, user_state: state('WAITING_ON_ME', 2) }],
        inbox_dimmed: [{ pull_request: gated, user_state: state('HIDDEN_GATE', 1) }],
        dependabot_prs: [{ pull_request: bump }],
      });
      expect(prs.map((p) => p.number).sort()).toEqual([1, 2, 3, 4]);
      expect(new Set(prs.map((p) => p.id)).size).toBe(4);
      expect(prs.find((p) => p.number === 1)).toMatchObject({ turn: true, owners: 'voce', ownFiles: 2 });
      expect(prs.find((p) => p.number === 4)).toMatchObject({ turn: false, owners: 'others' });
    });

    it('dims a PR of mine behind the gate and says why', () => {
      const gate = (extra: Record<string, unknown>) =>
        map({ seats: [seat([])], inbox_dimmed: [{ pull_request: pr(2, extra), user_state: state('HIDDEN_GATE', 1) }] })[0];

      const failing = gate({ ci_status: 'FAILURE' });
      expect(failing).toMatchObject({ turn: false, owners: 'voce', ownFiles: 1 });
      expect(failing.why).toEqual({ key: 'drawer.why.voce' });
      expect(failing.muteBadge).toEqual({ key: 'inbox.mute.ciFail' });
      expect(failing.muteTip).toEqual({ key: 'inbox.mute.ciFailTip' });

      expect(gate({ mergeable_state: 'CONFLICTING' }).muteBadge).toEqual({ key: 'inbox.mute.conflict' });
      expect(gate({ ci_status: 'PENDING' }).muteBadge).toEqual({ key: 'inbox.mute.ci' });
      expect(gate({ is_draft: true }).muteBadge).toEqual({ key: 'inbox.mute.draft' });
    });
  });

  describe('PR identity: the unique id is the key and the API handle, the GitHub number is only for display', () => {
    const dto = (id: number, number: number, repo: string, extra: Record<string, unknown> = {}) => ({
      id, repository_id: 1, repository_name: repo, number, title: `PR ${number}`, author_login: 'bia', html_url: '',
      is_dependabot: false, is_draft: false, state: 'OPEN', mergeable_state: 'CLEAN', ci_status: 'SUCCESS', head_sha: 'h', base_ref: 'main', ...extra,
    });
    const seat = (full: string, prs: unknown[]) => ({
      repository: { id: 1, owner: 'acme', name: full.split('/')[1], full_name: full, default_branch: 'main', codeowners_status: 'present' },
      open_pr_count: prs.length, waiting_on_me_count: 0, dependabot_pr_count: 0, failing_ci_count: 0, pull_requests: prs,
    });
    const state = (id: number) => ({ pull_request_id: id, user_id: 1, turn_state: 'WAITING_ON_ME', team_already_approved: false, owned_files_changed: [{ path: 'a.ts', additions: 1, deletions: 0, patch: '' }] });

    it('keeps two PRs with the same number in different repositories apart', () => {
      const a = dto(70, 7, 'acme/web');
      const b = dto(71, 7, 'acme/api');
      const prs = mapApiWorkspaceResponseToDataset({
        snapshot: { seats: [seat('acme/web', [a]), seat('acme/api', [b])], inbox_waiting_on_me: [{ pull_request: a, user_state: state(70) }] } as never,
        timeline: [],
      })!.prs;

      expect(prs.map((p) => [p.id, p.number, p.repo])).toEqual([[70, 7, 'acme/web'], [71, 7, 'acme/api']]);
    });

    it('uses the database id for every kind of PR, not the number the API routes cannot find', () => {
      const human = dto(23, 20, 'acme/web');
      const bump = dto(29, 24, 'acme/web', { is_dependabot: true, author_login: 'dependabot[bot]', title: 'bump x from 1.0.0 to 1.0.1' });
      const prs = mapApiWorkspaceResponseToDataset({
        snapshot: {
          seats: [seat('acme/web', [human, bump])],
          inbox_waiting_on_me: [{ pull_request: human, user_state: state(23) }],
          dependabot_prs: [{ pull_request: bump }],
        } as never,
        timeline: [],
      })!.prs;

      expect(prs.map((p) => [p.id, p.number])).toEqual([[23, 20], [29, 24]]);
    });

    it('keeps my approval of a Dependabot PR across a reload, and names a teammate who approved', () => {
      const bump = (id: number, number: number) => dto(id, number, 'acme/web', { is_dependabot: true, author_login: 'dependabot[bot]', title: 'bump x from 1.0.0 to 1.0.1' });
      const dataset = mapApiWorkspaceResponseToDataset({
        snapshot: {
          seats: [seat('acme/web', [])],
          dependabot_prs: [
            { pull_request: bump(30, 23), approved_by_me: true },
            { pull_request: bump(31, 22), approved_by_login: 'bia' },
            { pull_request: bump(32, 21) },
          ],
        } as never,
        timeline: [],
      })!;

      expect(dataset.approvedMap).toEqual({ 30: true });
      const [mine, team, none] = dataset.prs;
      expect(mine.approvedBy).toBeUndefined();
      expect(team.approvedBy).toBe('@bia');
      expect(none.approvedBy).toBeUndefined();
    });

    it('attaches who reviewed each PR, current reviews first as the server sends them', () => {
      const a = dto(70, 7, 'acme/web');
      const b = dto(71, 8, 'acme/web');
      const prs = mapApiWorkspaceResponseToDataset({
        snapshot: {
          seats: [seat('acme/web', [a, b])],
          reviews: {
            '70': [
              { reviewer: 'bob', state: 'APPROVED', commit_sha: 'h', submitted_at: '2026-11-10T09:00:00Z', current: true },
              { reviewer: 'carol', state: 'CHANGES_REQUESTED', commit_sha: 'old', submitted_at: '2026-11-09T09:00:00Z', current: false },
              { reviewer: 'dave', state: 'COMMENTED', commit_sha: 'h', submitted_at: '2026-11-10T10:00:00Z', current: true },
              { reviewer: 'erin', state: 'DISMISSED', commit_sha: 'h', submitted_at: '2026-11-10T11:00:00Z', current: true },
            ],
          },
        } as never,
        timeline: [],
      })!.prs;

      expect(prs.find((p) => p.id === 70)!.reviews).toEqual([
        { reviewer: 'bob', state: 'approved', current: true },
        { reviewer: 'carol', state: 'changes_requested', current: false },
        { reviewer: 'dave', state: 'commented', current: true },
        { reviewer: 'erin', state: 'dismissed', current: true },
      ]);
      expect(prs.find((p) => p.id === 71)!.reviews).toBeUndefined();
    });

    it('maps each PR\'s checks the way GitHub groups them, keeping only safe links', () => {
      const a = dto(70, 7, 'acme/web');
      const b = dto(71, 8, 'acme/web');
      const run = (name: string, status: string, conclusion: string, extra: Record<string, unknown> = {}) => ({
        id: 1, repository_id: 1, head_sha: 'h', name, status, conclusion, title: '', details_url: '', ...extra,
      });
      const prs = mapApiWorkspaceResponseToDataset({
        snapshot: {
          seats: [seat('acme/web', [a, b])],
          checks: {
            '70': [
              run('build', 'completed', 'failure', { title: 'Build failed', details_url: 'https://ci.example.com/1' }),
              run('cancelled-one', 'completed', 'cancelled'),
              run('timeout', 'completed', 'timed_out'),
              run('needs-you', 'completed', 'action_required'),
              run('test', 'in_progress', ''),
              run('queued-one', 'queued', ''),
              run('lint', 'completed', 'success', { title: 'Sem mudanças', details_url: 'javascript:alert(1)' }),
              run('docs', 'completed', 'neutral'),
              run('e2e', 'completed', 'skipped'),
            ],
          },
        } as never,
        timeline: [],
      })!.prs;

      const checks = prs.find((p) => p.id === 70)!.checks!;
      expect(checks.map((c) => [c.name, c.state])).toEqual([
        ['build', 'failure'], ['cancelled-one', 'failure'], ['timeout', 'failure'], ['needs-you', 'failure'],
        ['test', 'pending'], ['queued-one', 'pending'], ['lint', 'success'], ['docs', 'skipped'], ['e2e', 'skipped'],
      ]);
      expect(checks[0]).toMatchObject({ detail: 'Build failed', url: 'https://ci.example.com/1' });
      expect(checks.find((c) => c.name === 'lint')!.url).toBeUndefined(); // a javascript: link is never kept
      expect(prs.find((p) => p.id === 71)!.checks).toBeUndefined();
    });

    it('says why a bump that names several packages has no analysis', () => {
      const prs = mapApiWorkspaceResponseToDataset({
        snapshot: {
          seats: [seat('acme/web', [])],
          dependabot_prs: [
            { pull_request: dto(30, 23, 'acme/web', { is_dependabot: true, author_login: 'dependabot[bot]', title: 'build(deps): bump tinypool and vitest' }) },
            { pull_request: dto(31, 22, 'acme/web', { is_dependabot: true, author_login: 'dependabot[bot]', title: 'build(deps): bump undici from 7.29.0 to 7.30.2' }) },
          ],
        } as never,
        timeline: [],
      })!.prs;
      const [grouped, single] = prs;
      expect(grouped.pkg).toBeUndefined();
      expect(grouped.rvDetail).toEqual({ key: 'impact.rvDetail.grouped' });
      expect(single.pkg).toBe('undici');
      expect(single.rvDetail).toEqual({ key: 'impact.rvDetail.none' });
    });

    it('points an activity at its PR by id and writes the number in the sentence', () => {
      const item = mapActivityEventToTimelineItem({
        id: 'e', type: 'pr.approved', repository_name: 'acme/web', pull_request_id: 29, pr_number: 24, occurred_at: '2026-11-10T12:00:00Z',
      });
      expect(item.pr).toBe(29);
      expect(item.text).toMatchObject({ params: { pr: 24 } });

      const noPr = mapActivityEventToTimelineItem({ id: 'e2', type: 'repository.followed', repository_name: 'acme/web', occurred_at: '2026-11-10T12:00:00Z' });
      expect(noPr.pr).toBeUndefined();
    });
  });
});
