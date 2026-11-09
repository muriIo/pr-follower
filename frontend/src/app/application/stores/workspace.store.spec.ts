import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  LiveEventGateway,
  PULL_REQUEST_GATEWAY,
  PullRequestGateway,
} from '../../domain/gateways/workspace.gateways';
import { ActionCancellationManager } from '../use-cases/action-cancellation.manager';
import { WorkspaceStore } from './workspace.store';

function createFakeGateways(): { prGateway: PullRequestGateway; sseGateway: LiveEventGateway } {
  const prGateway: PullRequestGateway = {
    approvePullRequest: async () => undefined,
    mergePullRequest: async () => ({ sha: 'a1b2c3d' }),
    commentOnPullRequest: async () => undefined,
    snoozePullRequest: async () => undefined,
    syncRepositories: async () => undefined,
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

  it('executes PR approval, squash merge, and supports cooperative Escape cancellation', async () => {
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
});
