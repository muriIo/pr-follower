import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  LiveEventGateway,
  PULL_REQUEST_GATEWAY,
  PullRequestGateway,
} from '../../domain/gateways/workspace.gateways';
import { ActionError } from '../../domain/models/action-error.models';
import { PullRequestItem, RepoInfo, TimelineEventItem } from '../../domain/models/workspace.models';
import { ActionCancellationManager } from '../use-cases/action-cancellation.manager';
import { LocaleStore } from './locale.store';
import { WorkspaceStore } from './workspace.store';
import { createInitialPrDataset, loadWorkspaceFixture } from '../../testing/workspace-fixtures';

function createFakeGateways(): { prGateway: PullRequestGateway; sseGateway: LiveEventGateway } {
  const prGateway: PullRequestGateway = {
    fetchWorkspace: async () => null,
    approvePullRequest: async () => undefined,
    mergePullRequest: async () => ({ sha: undefined }),
    batchMergePullRequests: async (ids) => ({ mergedIds: ids }),
    commentOnPullRequest: async () => undefined,
    snoozePullRequest: async () => undefined,
    claimPullRequest: async () => undefined,
    cancelServerOperation: async () => undefined,
  };
  const sseGateway: LiveEventGateway = {
    connect: (_onEvent, onStateChange) => {
      onStateChange('conectado');
      return () => undefined;
    },
  };
  return { prGateway, sseGateway };
}

describe('WorkspaceStore (Clean Architecture Reactive Signal Store)', () => {
  it('computes Seats, CODEOWNERS Inbox, Dependabot Radar, and Inspection Drawer view models', () => {
    const { prGateway, sseGateway } = createFakeGateways();
    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: prGateway },
        { provide: LIVE_EVENT_GATEWAY, useValue: sseGateway },
      ],
    });

    const store = TestBed.inject(WorkspaceStore);
    loadWorkspaceFixture(store);
    expect(store.view()).toBe('seats');
    expect(store.seats().length).toBe(4);
    expect(store.navCounts().inbox).toBe(2);
    expect(store.navCounts().radar).toBe(7);

    // Filter seats
    store.setSeatFilter('turn');
    expect(store.seats().length).toBe(2);
    store.setSeatFilter('all');

    // Open CODEOWNERS PR #142 in drawer
    store.openPr(142);
    expect(store.selectedPrId()).toBe(142);
    const d142 = store.drawer()!;
    expect(d142.focused).toBe(true);
    expect(d142.banner).toContain('/src/app/');
    expect(d142.files[0].lines.length).toBeGreaterThan(10);

    // Open Dependabot PR #233 (7 blast files) in drawer
    store.openPr(233);
    const d233 = store.drawer()!;
    expect(d233.isDep).toBe(true);
    expect(d233.rvHead).toContain('Médio Risco (Score 46/100)');
    expect(d233.blasts.length).toBe(5);
    expect(d233.hasMore).toBe(true);
    store.showAllBlastFiles();
    expect(store.drawer()!.blasts.length).toBe(7);
  });

  it('executes PR approval, squash merge, loadWorkspace from API, and supports cooperative cancellation', async () => {
    const { prGateway, sseGateway } = createFakeGateways();
    const fetchWorkspaceSpy = vi.spyOn(prGateway, 'fetchWorkspace');

    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: prGateway },
        { provide: LIVE_EVENT_GATEWAY, useValue: sseGateway },
      ],
    });

    const store = TestBed.inject(WorkspaceStore);
    loadWorkspaceFixture(store);
    expect(fetchWorkspaceSpy).not.toHaveBeenCalled();
    store.startLiveSession();
    expect(fetchWorkspaceSpy).toHaveBeenCalled();

    store.openPr(142);

    // Approve PR #142
    await store.approvePr(142);
    expect(store.approvedMap()[142]).toBe(true);
    expect(store.navCounts().inbox).toBe(1);

    // Merge PR #142
    await store.mergePr(142);
    expect(store.mergedMap()[142]).toBe(true);

    // Start slow batch approve and cancel mid-flight via cancelAction('batch')
    const batchPromise = store.runBatchApprove();
    expect(store.batchAction().running).toBe(true);
    store.cancelAction('batch');
    await batchPromise;
    expect(store.batchAction().running).toBe(false);
    expect(store.batchAction().note).toContain('Lote cancelado');
  });

  it('supports docked inspection drawer resizing, clamping, keyboard stepping, and reset', () => {
    const { prGateway, sseGateway } = createFakeGateways();
    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: prGateway },
        { provide: LIVE_EVENT_GATEWAY, useValue: sseGateway },
      ],
    });

    const store = TestBed.inject(WorkspaceStore);
    store.resetDrawerWidth();
    expect(store.drawerWidth()).toBe(560);
    expect(store.isResizingDrawer()).toBe(false);

    store.startDrawerResize();
    expect(store.isResizingDrawer()).toBe(true);

    store.setDrawerWidth(680, 900);
    expect(store.drawerWidth()).toBe(680);

    // Clamps below minimum (420px) and above maximum
    store.setDrawerWidth(300, 900);
    expect(store.drawerWidth()).toBe(420);
    store.setDrawerWidth(1200, 820);
    expect(store.drawerWidth()).toBe(820);

    store.endDrawerResize();
    expect(store.isResizingDrawer()).toBe(false);

    // Keyboard step & reset
    store.resetDrawerWidth();
    expect(store.drawerWidth()).toBe(560);
    store.stepDrawerWidth(24, 900);
    expect(store.drawerWidth()).toBe(584);
    store.stepDrawerWidth(-48, 900);
    expect(store.drawerWidth()).toBe(536);
  });
});

describe('WorkspaceStore live session lifecycle (ADR-0010)', () => {
  it('stays idle until signed in, then loads and streams once, and disconnects on sign-out', () => {
    const { prGateway } = createFakeGateways();
    const fetchSpy = vi.spyOn(prGateway, 'fetchWorkspace');
    const disconnect = vi.fn();
    const connect = vi.fn((_onEvent, onState: (s: 'conectado') => void) => {
      onState('conectado');
      return disconnect;
    });

    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: prGateway },
        { provide: LIVE_EVENT_GATEWAY, useValue: { connect } },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();

    store.startLiveSession();
    store.startLiveSession();
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(connect).toHaveBeenCalledTimes(1);

    store.stopLiveSession();
    expect(disconnect).toHaveBeenCalledTimes(1);
    store.stopLiveSession();
    expect(disconnect).toHaveBeenCalledTimes(1);

    store.startLiveSession();
    expect(connect).toHaveBeenCalledTimes(2);
  });
});

describe('WorkspaceStore repository access (ADR-0010)', () => {
  it('flags seats the GitHub App no longer reaches and reloads when access changes live', async () => {
    const { prGateway } = createFakeGateways();
    let emit: ((evt: Omit<TimelineEventItem, 'key' | 't'>) => void) | null = null;
    const fetchWorkspace = vi.fn(async () => ({
      repos: [
        { name: 'acme/web', branch: 'main', access: 'removed' as const },
        { name: 'acme/api', branch: 'main', access: 'suspended' as const },
        { name: 'acme/ok', branch: 'main', access: 'active' as const },
      ],
      prs: [],
      events: [],
    }));

    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: { ...prGateway, fetchWorkspace } },
        {
          provide: LIVE_EVENT_GATEWAY,
          useValue: {
            connect: (onEvent: (evt: Omit<TimelineEventItem, 'key' | 't'>) => void) => {
              emit = onEvent;
              return () => undefined;
            },
          },
        },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    store.startLiveSession();
    await Promise.resolve();
    await Promise.resolve();

    const byName = Object.fromEntries(store.allSeats().map((s) => [s.name, s]));
    expect(byName['acme/web'].accessKey).toBe('seats.access.removed');
    expect(byName['acme/web'].cls).toContain('pf-seat-no-access');
    expect(byName['acme/api'].accessKey).toBe('seats.access.suspended');
    expect(byName['acme/ok'].accessKey).toBeNull();

    expect(fetchWorkspace).toHaveBeenCalledTimes(1);
    vi.useFakeTimers();
    try {
      emit!({ kind: 'fail', text: 'Lost access to acme/web', repo: 'acme/web', refreshWorkspace: true });
      vi.advanceTimersByTime(300);
      expect(fetchWorkspace).toHaveBeenCalledTimes(2);
      emit!({ kind: 'info', text: 'local note', repo: 'acme/ok' });
      vi.advanceTimersByTime(300);
      expect(fetchWorkspace).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('coalesces a burst of live changes into one workspace reload', () => {
    vi.useFakeTimers();
    try {
      const { prGateway } = createFakeGateways();
      const fetchWorkspace = vi.spyOn(prGateway, 'fetchWorkspace');
      let emit: ((evt: Omit<TimelineEventItem, 'key' | 't'>) => void) | null = null;
      TestBed.configureTestingModule({
        providers: [
          ActionCancellationManager,
          WorkspaceStore,
          { provide: PULL_REQUEST_GATEWAY, useValue: prGateway },
          {
            provide: LIVE_EVENT_GATEWAY,
            useValue: {
              connect: (onEvent: (evt: Omit<TimelineEventItem, 'key' | 't'>) => void) => {
                emit = onEvent;
                return () => undefined;
              },
            },
          },
        ],
      });
      const store = TestBed.inject(WorkspaceStore);
      store.startLiveSession();
      expect(fetchWorkspace).toHaveBeenCalledTimes(1);

      for (const pr of [1, 2, 3, 4, 5]) {
        emit!({ kind: 'info', text: `PR #${pr} opened`, repo: 'acme/web', pr, refreshWorkspace: true });
      }
      expect(fetchWorkspace).toHaveBeenCalledTimes(1);
      vi.advanceTimersByTime(300);
      expect(fetchWorkspace).toHaveBeenCalledTimes(2);

      store.stopLiveSession();
      emit!({ kind: 'info', text: 'late', repo: 'acme/web', refreshWorkspace: true });
      vi.advanceTimersByTime(300);
      expect(fetchWorkspace).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('WorkspaceStore live stream recovery', () => {
  it('reloads the workspace after the stream reconnects, since events sent meanwhile were missed', async () => {
    const { prGateway } = createFakeGateways();
    const fetchWorkspace = vi.spyOn(prGateway, 'fetchWorkspace');
    let setState: ((s: 'conectado' | 'reconectando' | 'desconectado') => void) | null = null;

    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: prGateway },
        {
          provide: LIVE_EVENT_GATEWAY,
          useValue: {
            connect: (_onEvent: unknown, onState: typeof setState) => {
              setState = onState;
              return () => undefined;
            },
          },
        },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    store.startLiveSession();
    expect(fetchWorkspace).toHaveBeenCalledTimes(1);

    setState!('conectado'); // first open: the initial load already covers it
    expect(fetchWorkspace).toHaveBeenCalledTimes(1);
    expect(store.connectionState()).toBe('conectado');

    setState!('reconectando');
    setState!('conectado');
    expect(fetchWorkspace).toHaveBeenCalledTimes(2);
  });
});

describe('WorkspaceStore truthful PR state', () => {
  function setup(repos: readonly RepoInfo[], prs: readonly PullRequestItem[]): WorkspaceStore {
    const { prGateway, sseGateway } = createFakeGateways();
    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: prGateway },
        { provide: LIVE_EVENT_GATEWAY, useValue: sseGateway },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    store.repos.set(repos);
    store.prs.set(prs);
    return store;
  }

  const base = createInitialPrDataset().find((p) => !p.dep)!;
  const pr = (id: number, repo: string, overrides: Partial<PullRequestItem>): PullRequestItem => ({
    ...base,
    id,
    repo,
    turn: false,
    approvedBy: undefined,
    muteBadge: undefined,
    size: undefined,
    ...overrides,
  });

  it('shows a repository without CI or CODEOWNERS as such, not as running or pending review', () => {
    const store = setup(
      [{ name: 'acme/bare', branch: 'main', codeowners: 'absent' }],
      [pr(11, 'acme/bare', { ci: 'none', owners: 'none', merge: 'unknown' })],
    );

    const seat = store.allSeats()[0];
    expect(seat.ciLabel).toBe('Sem CI');
    expect(seat.ciDot).toBe('pf-dot');
    expect(seat.noCodeowners).toBe(true);

    store.openPr(11);
    const drawer = store.drawer()!;
    expect(drawer.signals[0]).toEqual({ k: 'CI', v: 'Nenhum CI reportado para este commit', dot: 'pf-dot' });
    expect(drawer.signals[1]).toEqual({ k: 'CODEOWNERS', v: 'Repositório sem CODEOWNERS', dot: 'pf-dot' });
    // Neither missing CI nor missing CODEOWNERS blocks the merge, and we do not invent an approval rule.
    expect(drawer.blocked).toBe(false);
    expect(drawer.blockedText).toBe('');
    expect(drawer.mergeDis).toBe('false');
    // Unknown size is not shown as a signal.
    expect(drawer.signals.map((sig) => sig.k)).toEqual(['CI', 'CODEOWNERS']);
  });

  it('blocks a merge only for what GitHub reported: conflicts, out of date, branch protection, draft', () => {
    const store = setup(
      [{ name: 'acme/owned', branch: 'main', codeowners: 'present' }],
      [
        pr(21, 'acme/owned', { ci: 'run', owners: 'others', merge: 'clean' }),
        pr(22, 'acme/owned', { ci: 'fail', owners: 'others', merge: 'unstable' }),
        pr(23, 'acme/owned', { ci: 'ok', owners: 'ok', approvedBy: '@ana', merge: 'conflict' }),
        pr(24, 'acme/owned', { ci: 'ok', owners: 'ok', approvedBy: '@ana', merge: 'behind' }),
        pr(25, 'acme/owned', { ci: 'ok', owners: 'ok', approvedBy: '@ana', merge: 'blocked' }),
        pr(26, 'acme/owned', { ci: 'ok', owners: 'ok', approvedBy: '@ana', merge: 'draft' }),
        pr(27, 'acme/owned', { ci: 'ok', owners: 'ok', approvedBy: '@ana', merge: 'clean' }),
      ],
    );

    const seat = store.allSeats()[0];
    expect(seat.ciLabel).toBe('1 CI Falhando'); // failing CI is reported first
    expect(seat.noCodeowners).toBe(false);

    const blocked = (id: number) => {
      store.openPr(id);
      return store.drawer()!.blockedText;
    };
    // Checks still running are blockers (approving and merging wait for them); a failing check is
    // a signal GitHub lets through unless the branch requires it, so it is not a blocker we invent.
    expect(blocked(21)).toBe('Há checks em andamento: aguarde terminarem para aprovar ou mesclar.');
    expect(blocked(22)).toBe('');
    expect(blocked(23)).toBe('Mesclagem bloqueada: há conflitos com a branch base.');
    expect(blocked(24)).toBe('Mesclagem bloqueada: a branch está desatualizada em relação à base.');
    expect(blocked(25)).toBe('Mesclagem bloqueada pela proteção da branch no GitHub (revisões ou checks obrigatórios).');
    expect(blocked(26)).toBe('Mesclagem bloqueada: o PR ainda é rascunho.');
    expect(blocked(27)).toBe('');
  });

  it('shows an unchecked CODEOWNERS as unknown, never as a pending review', () => {
    const store = setup([{ name: 'acme/unchecked', branch: 'main' }], [pr(41, 'acme/unchecked', { owners: 'unknown', ownersText: '' })]);
    store.openPr(41);
    const drawer = store.drawer()!;
    expect(drawer.signals[1]).toEqual({ k: 'CODEOWNERS', v: 'CODEOWNERS ainda não verificado', dot: 'pf-dot' });
    expect(drawer.blocked).toBe(false);
  });

  it('lists every open PR of a repository in its seat, in rank order, never just the first few', () => {
    const prs = [1, 2, 3, 4, 5, 6].map((n) => pr(60 + n, 'acme/web', { number: n, rank: 7 - n }));
    const store = setup([{ name: 'acme/web', branch: 'main' }], prs);

    const stream = store.allSeats()[0].stream;
    expect(stream.map((row) => row.number)).toEqual([6, 5, 4, 3, 2, 1]); // lowest rank value first
    expect(stream).toHaveLength(6);
  });

  it('hides the risk badge on PRs that have no risk verdict', () => {
    const store = setup([{ name: 'acme/web', branch: 'main' }], [pr(51, 'acme/web', { verdict: null })]);
    store.openPr(51);
    expect(store.drawer()!.hasVerdict).toBe(false);
    const row = store.allSeats()[0].stream[0];
    expect(row.showRisk).toBe(false);
  });

  it('previews every changed file as its own collapsible card, or says the preview is not available', () => {
    const files = [
      { path: 'src/a.ts', additions: 3, deletions: 1, hunk: [{ t: 'hdr' as const, s: '@@ -1 +1 @@' }, { t: 'add' as const, s: '+ new' }] },
      { path: 'src/b.ts', additions: 1, deletions: 0, hunk: [] },
    ];
    const store = setup([{ name: 'acme/web', branch: 'main' }], [pr(61, 'acme/web', { files }), pr(62, 'acme/web', { files: [] })]);

    store.openPr(61);
    let drawer = store.drawer()!;
    expect(drawer.hasFiles).toBe(true);
    expect(drawer.files.map((f) => [f.path, f.deltaAdd, f.deltaDel, f.expanded])).toEqual([
      ['src/a.ts', '+3', '-1', true],
      ['src/b.ts', '+1', '-0', false],
    ]);
    expect(drawer.files[0].lines.length).toBe(2);
    expect(drawer.files[1].hasDiff).toBe(false);

    store.toggleChangedFile(1);
    drawer = store.drawer()!;
    expect(drawer.files[1].expanded).toBe(true);

    store.openPr(62);
    drawer = store.drawer()!;
    expect(drawer.hasFiles).toBe(false);
    expect(drawer.files).toEqual([]);
  });

  it('groups Dependabot PRs without analysis apart and never batch-merges them', () => {
    const dep = createInitialPrDataset().find((p) => p.dep)!;
    const store = setup(
      [{ name: 'acme/web', branch: 'main' }],
      [{ ...dep, id: 71, repo: 'acme/web', ci: 'ok', verdict: null, depType: 'devDependencies' }],
    );
    const groups = store.radarGroups();
    expect(groups.map((g) => g.label)).toEqual(['⚪ Sem análise — risco ainda não avaliado (1)']);
    expect(store.safeBatchSummary().has).toBe(false);
  });

  it('labels cards without CI as "Sem CI" in the inbox and the radar', () => {
    const dep = createInitialPrDataset().find((p) => p.dep)!;
    const store = setup(
      [{ name: 'acme/bare', branch: 'main', codeowners: 'absent' }],
      [
        pr(31, 'acme/bare', { ci: 'none', owners: 'none', muteBadge: '🚧 gate' }),
        { ...dep, id: 32, repo: 'acme/bare', ci: 'none' },
      ],
    );
    expect(store.mutedInboxCards().find((c) => c.id === 31)!.ciLabel).toBe('Sem CI');
    const radar = store.radarGroups().flatMap((g) => g.items);
    expect(radar.find((c) => c.id === 32)!.ciShort).toBe('Sem CI');
    // A dependency bump nothing has verified is not a safe batch candidate.
    expect(store.safeBatchSummary().safeApproved.concat(store.safeBatchSummary().safePending)).toEqual([]);
  });
});

describe('WorkspaceStore changed-files preview', () => {
  const file = (path: string, owned: boolean) => ({ path, additions: 1, deletions: 0, hunk: [], owned });

  function setup(fetchChangedFiles: PullRequestGateway['fetchChangedFiles']) {
    const { prGateway, sseGateway } = createFakeGateways();
    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: { ...prGateway, fetchChangedFiles } },
        { provide: LIVE_EVENT_GATEWAY, useValue: sseGateway },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    loadWorkspaceFixture(store);
    return store;
  }

  it('loads the files when a PR opens, opening the first file the user owns', async () => {
    const fetch = vi.fn(async () => [file('README.md', false), file('src/app/seat.ts', true), file('src/app/x.ts', true)]);
    const store = setup(fetch);

    store.openPr(142);
    expect(store.drawer()!.filesState).toBe('loading');
    await vi.waitFor(() => expect(store.drawer()!.filesState).toBe('ready'));

    expect(fetch).toHaveBeenCalledWith('arvore/hive-web', 142, expect.objectContaining({ signal: expect.any(AbortSignal) }));
    const files = store.drawer()!.files;
    expect(files.map((f) => [f.path, f.owned, f.expanded])).toEqual([
      ['README.md', false, false],
      ['src/app/seat.ts', true, true],
      ['src/app/x.ts', true, false],
    ]);
  });

  it('cancels the previous load when another PR opens or the drawer closes', async () => {
    const signals: AbortSignal[] = [];
    const store = setup(
      vi.fn((_repo: string, _n: number, options?: { signal?: AbortSignal }) => {
        signals.push(options!.signal!);
        return new Promise<never>(() => undefined);
      }),
    );
    store.openPr(142);
    store.openPr(139);
    expect(signals[0].aborted).toBe(true);
    store.closeDrawer();
    expect(signals[1].aborted).toBe(true);
  });

  it('reports a failed load instead of an empty list', async () => {
    const store = setup(vi.fn(async () => Promise.reject(new Error('502'))));
    store.openPr(142);
    await vi.waitFor(() => expect(store.drawer()!.filesState).toBe('error'));
    expect(store.drawer()!.files).toEqual([]);
  });

  it('does not load files for Dependabot PRs, whose drawer shows the impact analysis', () => {
    const fetch = vi.fn(async () => []);
    const store = setup(fetch);
    const dep = store.prs().find((p) => p.dep)!;
    store.openPr(dep.id);
    expect(fetch).not.toHaveBeenCalled();
  });
});

describe('WorkspaceStore timeline views', () => {
  const now = Date.now();
  const ev = (id: string, minutesAgo: number, repo: string, actor: string, type = 'pr.opened'): TimelineEventItem => ({
    key: minutesAgo,
    id,
    t: now - minutesAgo * 60_000,
    kind: 'info',
    type,
    actor,
    text: id,
    repo,
  });

  function setup(events: readonly TimelineEventItem[], onEvent?: (emit: (e: Omit<TimelineEventItem, 'key' | 't'>) => void) => void) {
    const { prGateway } = createFakeGateways();
    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: { ...prGateway, fetchWorkspace: async () => ({ repos: [], prs: [], events }) } },
        {
          provide: LIVE_EVENT_GATEWAY,
          useValue: {
            connect: (emit: (e: Omit<TimelineEventItem, 'key' | 't'>) => void) => {
              onEvent?.(emit);
              return () => undefined;
            },
          },
        },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    store.repos.set([
      { name: 'acme/web', branch: 'main' },
      { name: 'acme/api', branch: 'main' },
    ]);
    store.events.set(events);
    return store;
  }

  const flat = (store: WorkspaceStore) => store.timelineGroups().flatMap((g) => g.items.map((i) => i.text));

  it('phrases server activity in the current language', () => {
    const store = setup([{ ...ev('x', 1, 'acme/web', 'bia'), text: { key: 'timeline.event.pr.opened', params: { pr: 7, title: 'feat: x' } } }]);
    expect(flat(store)).toEqual(['Abriu #7: feat: x']);
  });

  it('lists the newest activity first, grouped from now to older', () => {
    const store = setup([
      ev('two-days', 60 * 50, 'acme/web', 'bia'),
      ev('now', 1, 'acme/web', 'bia'),
      ev('half-hour', 30, 'acme/api', 'bia'),
    ]);
    expect(flat(store)).toEqual(['now', 'half-hour', 'two-days']);
    const labels = store.timelineGroups().map((g) => g.label);
    expect(labels[0]).toBe('Agora');
    expect(labels[1]).toBe('Última hora');
    expect(labels[labels.length - 1]).toBe('Mais antigas');
  });

  it('filters to my activity or to one repository of my Seats', () => {
    const store = setup([
      ev('mine-web', 1, 'acme/web', 'octocat'),
      ev('theirs-web', 2, 'acme/web', 'bia'),
      ev('theirs-api', 3, 'acme/api', 'bia'),
    ]);
    store.startLiveSession('Octocat');

    store.setTimelineScope('mine');
    expect(flat(store)).toEqual(['mine-web']);

    store.setTimelineScope('all');
    store.setTimelineRepo('acme/web');
    expect(flat(store)).toEqual(['mine-web', 'theirs-web']);
    expect(store.timelineRepoOptions()).toEqual(['acme/api', 'acme/web']);

    store.setTimelineRepo('acme/missing');
    expect(store.timelineGroups()).toEqual([]);
    store.setTimelineRepo(null);
    expect(flat(store).length).toBe(3);
  });

  it('gives every item its activity icon', () => {
    const store = setup([ev('merged', 1, 'acme/web', 'bia', 'pr.merged'), ev('opened', 2, 'acme/web', 'bia', 'pr.opened')]);
    const items = store.timelineGroups()[0].items;
    expect(items.map((i) => [i.icon, i.tone])).toEqual([
      ['merge', 'merged'],
      ['pr-open', 'open'],
    ]);
  });

  it('keeps live activity highlighted after the workspace reloads, and attributes my own actions to me', async () => {
    let emit: ((e: Omit<TimelineEventItem, 'key' | 't'>) => void) | null = null;
    const store = setup([], (e) => (emit = e));
    store.startLiveSession('octocat');
    emit!({ id: 'live-1', type: 'pr.opened', kind: 'info', text: 'live', repo: 'acme/web', actor: 'bia', fresh: true });
    expect(store.timelineGroups()[0].items[0].cls).toBe('is-new');

    store.events.set([{ ...ev('live-1', 0, 'acme/web', 'bia'), fresh: false }]); // what a reload brings back
    expect(store.timelineGroups()[0].items[0].cls).toBe('is-new');

    const pr = { ...createInitialPrDataset()[0], id: 501, repo: 'acme/web' };
    store.prs.set([pr]);
    await store.approvePr(501);
    const mine = store.events().find((e) => e.type === 'pr.approved')!;
    expect(mine.actor).toBe('octocat');
  });
});


describe('WorkspaceStore acting on GitHub as the user', () => {
  const base = createInitialPrDataset().find((p) => !p.dep)!;
  const prOf = (id: number, author: string): PullRequestItem => ({ ...base, id, repo: 'acme/web', author, approvedBy: undefined });

  function setup(overrides: Partial<PullRequestGateway>) {
    const { prGateway, sseGateway } = createFakeGateways();
    const gateway = { ...prGateway, approvePullRequest: vi.fn(prGateway.approvePullRequest), mergePullRequest: vi.fn(prGateway.mergePullRequest), ...overrides };
    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: gateway },
        { provide: LIVE_EVENT_GATEWAY, useValue: sseGateway },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    store.startLiveSession('octocat');
    store.repos.set([{ name: 'acme/web', branch: 'main', access: 'active' }]);
    store.prs.set([prOf(1, '@bia'), prOf(2, '@OctoCat')]);
    return { store, gateway, translate: (key: string, params?: Record<string, string | number>) => TestBed.inject(LocaleStore).translate(key, params) };
  }

  it('never approves the user\'s own PR: nothing is sent, and the drawer does not offer it', async () => {
    const { store, gateway, translate } = setup({});

    store.openPr(2);
    expect(store.drawer()!.showApprove).toBe(false);
    expect(store.drawer()!.ownNote).toBe(translate('drawer.ownPr'));

    await store.approvePr(2);
    expect(gateway.approvePullRequest).not.toHaveBeenCalled();
    expect(store.approvedMap()[2]).toBeUndefined();
    expect(store.drawer()!.act.note).toBe(translate('actions.error.own_pull_request'));

    store.openPr(1);
    expect(store.drawer()!.showApprove).toBe(true);
    expect(store.drawer()!.ownNote).toBe('');
  });

  it('lets the user merge their own PR', async () => {
    const { store, gateway } = setup({});
    await store.mergePr(2);
    expect(gateway.mergePullRequest).toHaveBeenCalledTimes(1);
    expect(store.mergedMap()[2]).toBe(true);
  });

  it('tells the user when GitHub could not confirm an action, and records nothing', async () => {
    const { store, translate } = setup({
      approvePullRequest: vi.fn(async () => {
        throw new ActionError('outcome_unknown', 'could not confirm');
      }),
    });

    await store.approvePr(1);
    expect(store.approvedMap()[1]).toBeUndefined();
    expect(store.events().some((e) => e.type === 'pr.approved')).toBe(false);
    store.openPr(1);
    const note = store.drawer()!.act;
    expect(note.note).toBe(translate('actions.error.outcome_unknown'));
    expect(note.noteCls).toBe('pf-note pf-note-err');
  });

  it('shows GitHub\'s own explanation when it refuses a merge, and does not mark it merged', async () => {
    const { store, translate } = setup({
      mergePullRequest: vi.fn(async () => {
        throw new ActionError('conflict', 'Pull Request is not mergeable');
      }),
    });

    await store.mergePr(1);
    expect(store.mergedMap()[1]).toBeUndefined();
    store.openPr(1);
    expect(store.drawer()!.act.note).toBe(translate('actions.error.conflict', { detail: 'Pull Request is not mergeable' }));
  });

  it('says "merged" without a made-up commit when GitHub returned none', async () => {
    const { store, translate } = setup({});
    store.openPr(1);
    await store.mergePr(1);
    // The drawer closes with the merge; what is left to say is the plain notice, with no commit made up.
    expect(store.notice()).toBe(translate('notice.pr.mergedByYou', { pr: store.prs().find((p) => p.id === 1)!.number }));
  });

  it('does not wait on artificial delays: an action finishes as soon as the server answers', async () => {
    vi.useFakeTimers();
    try {
      const { store } = setup({});
      const done = store.approvePr(1);
      await vi.advanceTimersByTimeAsync(0);
      await done;
      expect(store.approvedMap()[1]).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('analyses a Dependabot PR on demand and reloads the workspace to show the result', async () => {
    const analyzePullRequest = vi.fn(async () => undefined);
    const fetchWorkspace = vi.fn(async () => null);
    const { store, translate } = setup({ analyzePullRequest, fetchWorkspace });
    store.prs.set([{ ...prOf(9, '@dependabot[bot]'), dep: true, verdict: null, pkg: 'vitest' }]);
    store.openPr(9);
    expect(store.drawer()!.showAnalyze).toBe(true);

    await store.analyzePr(9);
    expect(analyzePullRequest).toHaveBeenCalledWith(9, expect.anything());
    expect(fetchWorkspace).toHaveBeenCalled();
    expect(store.drawer()!.act.note).toBe(translate('actions.analyzed'));
  });

  it('offers the analysis only for Dependabot PRs that have none', () => {
    const { store } = setup({});
    store.prs.set([
      { ...prOf(9, '@dependabot[bot]'), dep: true, verdict: 'baixo', pkg: 'vitest' },
      { ...prOf(10, '@dependabot[bot]'), dep: true, verdict: null, pkg: 'vitest' },
      prOf(11, '@bia'),
      // "bump tinypool and vitest": no single package, so there is nothing the analysis could read.
      { ...prOf(12, '@dependabot[bot]'), dep: true, verdict: null, pkg: undefined },
    ]);
    const flags = [9, 10, 11, 12].map((id) => {
      store.openPr(id);
      return store.drawer()!.showAnalyze;
    });
    expect(flags).toEqual([false, true, false, false]);
  });

  it('tells the user when the analysis is unavailable or fails', async () => {
    const { store, translate } = setup({
      analyzePullRequest: vi.fn(async () => {
        throw new ActionError('unavailable');
      }),
    });
    store.prs.set([{ ...prOf(9, '@dependabot[bot]'), dep: true, verdict: null, pkg: 'vitest' }]);
    store.openPr(9);
    await store.analyzePr(9);
    expect(store.drawer()!.act.note).toBe(translate('actions.error.unavailable'));
  });

  it('explains why a PR cannot be analysed instead of calling the request invalid', async () => {
    const { store, translate } = setup({
      analyzePullRequest: vi.fn(async () => {
        throw new ActionError('not_analysable');
      }),
    });
    store.prs.set([{ ...prOf(9, '@dependabot[bot]'), dep: true, verdict: null, pkg: 'vitest' }]);
    store.openPr(9);
    await store.analyzePr(9);
    expect(store.drawer()!.act.note).toBe(translate('actions.error.not_analysable'));
    expect(translate('actions.error.not_analysable')).not.toBe(translate('actions.error.invalid_input'));
  });

  it('acts on the PR that was shown even when another repository has a PR with the same number', async () => {
    const fetchChangedFiles = vi.fn(async () => []);
    const { store, gateway } = setup({ fetchChangedFiles });
    store.prs.set([
      { ...prOf(70, '@bia'), number: 7, repo: 'acme/web' },
      { ...prOf(71, '@bia'), number: 7, repo: 'acme/api' },
    ]);

    await store.approvePr(71);
    expect(gateway.approvePullRequest).toHaveBeenCalledWith(71, expect.anything());
    expect(store.approvedMap()[71]).toBe(true);
    expect(store.approvedMap()[70]).toBeUndefined();

    // GitHub's files are read by number, in the PR's own repository.
    await store.loadChangedFiles(71);
    expect(fetchChangedFiles).toHaveBeenCalledWith('acme/api', 7, expect.anything());
  });

  it('shows the GitHub number, not the id, to the user', async () => {
    const { store } = setup({});
    store.prs.set([{ ...prOf(71, '@bia'), number: 7, repo: 'acme/api' }]);

    store.openPr(71);
    expect(store.drawer()!.number).toBe(7);
    expect(store.drawer()!.id).toBe(71);

    await store.approvePr(71);
    const local = store.events().find((e) => e.type === 'pr.approved')!;
    expect(local.pr).toBe(71);
    expect(local.text).toMatchObject({ params: { pr: 7 } });
  });

  it('shows who reviewed the open PR, saying which reviews are of an older commit', () => {
    const { store, translate } = setup({});
    store.prs.set([
      {
        ...prOf(1, '@bia'),
        reviews: [
          { reviewer: 'bob', state: 'approved', current: true },
          { reviewer: 'carol', state: 'changes_requested', current: false },
          { reviewer: 'dave', state: 'commented', current: true },
          { reviewer: 'erin', state: 'dismissed', current: true },
        ],
      },
      prOf(2, '@bia'),
    ]);

    store.openPr(1);
    const d = store.drawer()!;
    expect(d.hasReviews).toBe(true);
    expect(d.reviews.map((r) => [r.reviewer, r.label, r.stale])).toEqual([
      ['@bob', translate('drawer.review.approved'), ''],
      ['@carol', translate('drawer.review.changes_requested'), translate('drawer.review.stale')],
      ['@dave', translate('drawer.review.commented'), ''],
      ['@erin', translate('drawer.review.dismissed'), ''],
    ]);
    expect(d.reviews[0].badgeCls).toBe('pf-badge-baixo');
    expect(d.reviews[1].badgeCls).toBe('pf-badge-alto');

    store.openPr(2);
    expect(store.drawer()!.hasReviews).toBe(false);
  });
});

describe('WorkspaceStore keeps the drawer true to its PR', () => {
  const base = createInitialPrDataset().find((p) => !p.dep)!;
  const prOf = (id: number, number: number, overrides: Partial<PullRequestItem> = {}): PullRequestItem => ({
    ...base, id, number, repo: 'acme/web', author: '@bia', approvedBy: undefined, ...overrides,
  });

  function setup(fetchWorkspace: PullRequestGateway['fetchWorkspace'] = async () => null) {
    const { prGateway } = createFakeGateways();
    let emit!: (e: Omit<TimelineEventItem, 'key' | 't'>) => void;
    const sse: LiveEventGateway = {
      connect: (onEvent, onState) => {
        emit = onEvent;
        onState('conectado');
        return () => undefined;
      },
    };
    const gateway = {
      ...prGateway,
      fetchWorkspace,
      snoozePullRequest: vi.fn(prGateway.snoozePullRequest),
      approvePullRequest: vi.fn(prGateway.approvePullRequest),
      mergePullRequest: vi.fn(prGateway.mergePullRequest),
    };
    TestBed.resetTestingModule();
    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        { provide: PULL_REQUEST_GATEWAY, useValue: gateway },
        { provide: LIVE_EVENT_GATEWAY, useValue: sse },
      ],
    });
    const store = TestBed.inject(WorkspaceStore);
    store.startLiveSession('octocat');
    store.repos.set([{ name: 'acme/web', branch: 'main', access: 'active' }]);
    store.prs.set([prOf(1, 11), prOf(2, 12)]);
    const t = (key: string, params?: Record<string, string | number>) => TestBed.inject(LocaleStore).translate(key, params);
    return { store, emit: (e: Partial<TimelineEventItem>) => emit({ kind: 'info', text: '', repo: 'acme/web', ...e } as never), gateway, t };
  }

  it('closes the drawer, and says why, when its PR is no longer open after a reload', async () => {
    const dataset = { repos: [{ name: 'acme/web', branch: 'main', access: 'active' as const }], prs: [prOf(2, 12)], events: [] };
    const { store, t } = setup(async () => dataset);
    store.openPr(1);
    expect(store.drawer()).not.toBeNull();

    await store.loadWorkspace(); // PR 1 was merged or closed meanwhile: it is not in the open list

    expect(store.selectedPrId()).toBeNull(); // the layout must stop reserving room for it too
    expect(store.drawer()).toBeNull();
    expect(store.notice()).toBe(t('notice.pr.gone', { pr: 11 }));
  });

  it('keeps the drawer open, updated, when its PR is still open after a reload', async () => {
    const updated = prOf(1, 11, { title: 'a new title after a push' });
    const { store } = setup(async () => ({ repos: [{ name: 'acme/web', branch: 'main', access: 'active' as const }], prs: [updated], events: [] }));
    store.openPr(1);

    await store.loadWorkspace();

    expect(store.selectedPrId()).toBe(1);
    expect(store.drawer()!.title).toBe('a new title after a push');
    expect(store.notice()).toBeNull();
  });

  it('closes the drawer the moment GitHub says its PR was merged or closed, without waiting for the reload', () => {
    const merged = setup();
    merged.store.openPr(1);
    merged.emit({ type: 'pr.merged', pr: 1 });
    expect(merged.store.selectedPrId()).toBeNull();
    expect(merged.store.notice()).toBe(merged.t('notice.pr.merged', { pr: 11 }));

    const closed = setup();
    closed.store.openPr(1);
    closed.emit({ type: 'pr.closed', pr: 1 });
    expect(closed.store.drawer()).toBeNull();
    expect(closed.store.notice()).toBe(closed.t('notice.pr.closed', { pr: 11 }));

    // Activity about another PR leaves the open drawer alone.
    const other = setup();
    other.store.openPr(1);
    other.emit({ type: 'pr.merged', pr: 2 });
    expect(other.store.selectedPrId()).toBe(1);
    expect(other.store.notice()).toBeNull();
  });

  it('closes the drawer after the user merges its PR, so nothing is left to approve or merge', async () => {
    const { store, t } = setup();
    store.openPr(1);
    await store.mergePr(1);

    expect(store.selectedPrId()).toBeNull();
    expect(store.notice()).toBe(t('notice.pr.mergedByYou', { pr: 11 }));
  });

  it('lets the user dismiss the notice', () => {
    const { store, emit } = setup();
    store.openPr(1);
    emit({ type: 'pr.merged', pr: 1 });
    expect(store.notice()).not.toBeNull();
    store.dismissNotice();
    expect(store.notice()).toBeNull();
  });

  it('does not offer "silence until CI passes" once CI has passed, and says why', async () => {
    const { store, gateway, t } = setup();
    store.prs.set([prOf(1, 11, { ci: 'ok' }), prOf(2, 12, { ci: 'run' }), prOf(3, 13, { ci: 'fail' }), prOf(4, 14, { ci: 'none' })]);

    const view = (id: number) => {
      store.openPr(id);
      const d = store.drawer()!;
      return [d.silenceCiDis, d.silenceCiTip];
    };
    expect(view(1)).toEqual(['true', t('drawer.silenceCi.passed')]);
    expect(view(4)).toEqual(['true', t('drawer.silenceCi.none')]); // nothing will ever report: it would never wake
    expect(view(2)).toEqual(['false', '']);
    expect(view(3)).toEqual(['false', '']);

    // A disabled button can still be reached by keyboard: the store refuses too.
    await store.silencePr(1, 'ci');
    expect(gateway.snoozePullRequest).not.toHaveBeenCalled();
    await store.silencePr(2, 'ci');
    expect(gateway.snoozePullRequest).toHaveBeenCalledTimes(1);
  });

  it('shows a PR\'s checks like GitHub\'s panel: a verdict, the counts, and the failing ones first', () => {
    const { store, t: translate } = setup();
    const check = (name: string, state: 'failure' | 'pending' | 'success' | 'skipped', detail = '', url?: string) => ({ name, state, detail, url });
    store.prs.set([
      {
        ...prOf(1, 11),
        checks: [
          check('api-iam', 'failure', 'Checks falharam', 'https://ci.example.com/iam'),
          check('api-learning', 'failure'),
          check('backoffice', 'success', 'Sem mudanças'),
          check('tenant', 'success'),
          check('web', 'success'),
          check('slow', 'pending'),
          check('docs', 'skipped'),
        ],
      },
      { ...prOf(2, 12), checks: [check('build', 'success'), check('test', 'success')] },
      { ...prOf(3, 13), checks: [check('build', 'success'), check('test', 'pending')] },
      prOf(4, 14),
    ]);

    const view = (id: number) => {
      store.openPr(id);
      return store.drawer()!.checks;
    };

    const mixed = view(1);
    expect(mixed.has).toBe(true);
    expect(mixed.tone).toBe('fail');
    expect(mixed.title).toBe(translate('drawer.checks.title.fail'));
    expect(mixed.subtitle).toBe(
      [translate('drawer.checks.count.failing', { count: 2 }), translate('drawer.checks.count.running', { count: 1 }),
       translate('drawer.checks.count.successful', { count: 3 }), translate('drawer.checks.count.skipped', { count: 1 })].join(', '),
    );
    expect(mixed.groups.map((g) => [g.key, g.items.length, g.open])).toEqual([
      ['failure', 2, true], ['pending', 1, true], ['success', 3, false], ['skipped', 1, false],
    ]);
    expect(mixed.groups[0].label).toBe(translate('drawer.checks.group.failure.other', { count: 2 }));
    expect(mixed.groups[1].label).toBe(translate('drawer.checks.group.pending.one', { count: 1 }));
    expect(mixed.groups[0].items[0]).toMatchObject({ name: 'api-iam', detail: 'Checks falharam', url: 'https://ci.example.com/iam' });

    const allGreen = view(2);
    expect(allGreen.tone).toBe('ok');
    expect(allGreen.title).toBe(translate('drawer.checks.title.ok'));
    expect(allGreen.groups.map((g) => g.key)).toEqual(['success']);

    const running = view(3);
    expect(running.tone).toBe('run');
    expect(running.title).toBe(translate('drawer.checks.title.run'));

    // Nothing reported: no panel, and nothing claimed.
    expect(view(4).has).toBe(false);
  });

  it('never lets the CI signal contradict the checks panel: with checks listed, the signal says what the panel says', () => {
    const { store, t } = setup();
    const failing = [{ name: 'api-iam', state: 'failure' as const, detail: '' }, { name: 'web', state: 'success' as const, detail: '' }];
    store.prs.set([prOf(1, 11, { ci: 'ok', checks: failing }), prOf(2, 12, { ci: 'ok' })]);

    store.openPr(1);
    const ci = store.drawer()!.signals.find((sig) => sig.k === 'CI')!;
    expect(ci.v).toBe(t('drawer.checks.title.fail'));
    expect(ci.dot).toContain('pf-dot-fail');

    // Without checks the signal is the PR's own CI state, as before.
    store.openPr(2);
    expect(store.drawer()!.signals.find((sig) => sig.k === 'CI')!.dot).toContain('pf-dot-ok');
  });

  it('blocks approving and merging while checks are still running, and says why', async () => {
    const { store, gateway, t } = setup();
    store.prs.set([
      prOf(1, 11, { ci: 'run' }),
      prOf(2, 12, { ci: 'ok' }),
      prOf(3, 13, { ci: 'fail' }), // a failure is a warning, not a blocker
      prOf(4, 14, { ci: 'none' }),
    ]);
    const view = (id: number) => {
      store.openPr(id);
      const d = store.drawer()!;
      return { approve: d.approveDis, approveTip: d.approveTip, merge: d.mergeDis, blocked: d.blocked, text: d.blockedText };
    };

    expect(view(1)).toEqual({ approve: 'true', approveTip: t('drawer.checksRunning'), merge: 'true', blocked: true, text: t('drawer.checksRunning') });
    for (const id of [2, 3, 4]) {
      expect(view(id)).toMatchObject({ approve: 'false', approveTip: '', merge: 'false' });
    }

    // A keyboard can still reach a disabled button: the store refuses, nothing is sent, the user is told.
    store.openPr(1);
    await store.approvePr(1);
    expect(gateway.approvePullRequest).not.toHaveBeenCalled();
    expect(store.drawer()!.act.note).toBe(t('actions.error.checks_running'));
    await store.mergePr(1);
    expect(gateway.mergePullRequest).not.toHaveBeenCalled();
    expect(store.mergedMap()[1]).toBeUndefined();

    await store.approvePr(2);
    expect(gateway.approvePullRequest).toHaveBeenCalledTimes(1);
  });

  it('explains a "checks running" answer from the server, for checks that started after the screen was drawn', async () => {
    const { store, t } = setup();
    store.prs.set([prOf(1, 11, { ci: 'ok' })]); // the screen still says green
    const gateway = TestBed.inject(PULL_REQUEST_GATEWAY) as PullRequestGateway;
    (gateway as { approvePullRequest: unknown }).approvePullRequest = vi.fn(async () => {
      throw new ActionError('checks_running');
    });

    store.openPr(1);
    await store.approvePr(1);

    expect(store.drawer()!.act.note).toBe(t('actions.error.checks_running'));
    expect(store.approvedMap()[1]).toBeUndefined();
  });
});
