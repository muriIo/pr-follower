import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  LiveEventGateway,
  PULL_REQUEST_GATEWAY,
  PullRequestGateway,
} from '../../domain/gateways/workspace.gateways';
import { TimelineEventItem } from '../../domain/models/workspace.models';
import { ActionCancellationManager } from '../use-cases/action-cancellation.manager';
import { WorkspaceStore } from './workspace.store';
import { loadWorkspaceFixture } from '../../testing/workspace-fixtures';

function createFakeGateways(): { prGateway: PullRequestGateway; sseGateway: LiveEventGateway } {
  const prGateway: PullRequestGateway = {
    fetchWorkspace: async () => null,
    approvePullRequest: async () => undefined,
    mergePullRequest: async () => ({ sha: 'a1b2c3d' }),
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
    store.setSeatFilter('todos');

    // Open CODEOWNERS PR #142 in drawer
    store.openPr(142);
    expect(store.selectedPrId()).toBe(142);
    const d142 = store.drawer()!;
    expect(d142.focused).toBe(true);
    expect(d142.banner).toContain('/src/app/');
    expect(d142.lines.length).toBeGreaterThan(10);

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
    await store.approvePr(142, 5);
    expect(store.approvedMap()[142]).toBe(true);
    expect(store.navCounts().inbox).toBe(1);

    // Merge PR #142
    await store.mergePr(142, 5);
    expect(store.mergedMap()[142]).toBe(true);

    // Start slow batch approve and cancel mid-flight via cancelAction('batch')
    const batchPromise = store.runBatchApprove(200);
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
