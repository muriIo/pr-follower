import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../../domain/gateways/workspace.gateways';
import { WorkspaceStore } from '../../../../application/stores/workspace.store';
import { DependabotImpactComponent } from './dependabot-impact.component';
import { loadWorkspaceFixture } from '../../../../testing/workspace-fixtures';

describe('DependabotImpactComponent', () => {
  it('renders Dependabot risk verdict, dependency path tree, blast radius files, and breaking notes', () => {
    TestBed.configureTestingModule({
      imports: [DependabotImpactComponent],
      providers: [
        WorkspaceStore,
        {
          provide: PULL_REQUEST_GATEWAY,
          useValue: {
            fetchWorkspace: async () => null,
            approvePullRequest: async () => undefined,
            mergePullRequest: async () => ({ sha: 'a1b2c3d' }),
            commentOnPullRequest: async () => undefined,
            snoozePullRequest: async () => undefined,
            cancelServerOperation: async () => undefined,
          },
        },
        {
          provide: LIVE_EVENT_GATEWAY,
          useValue: {
            connect: (_onEvent: unknown, onState: (s: 'conectado') => void) => {
              onState('conectado');
              return () => undefined;
            },
          },
        },
        {
          provide: TRANSLATION_GATEWAY,
          useValue: { loadDictionary: async () => ({}) },
        },
      ],
    });

    const store = TestBed.inject(WorkspaceStore);

    loadWorkspaceFixture(store);
    store.openPr(233);
    const drawerVm = store.drawer()!;

    const fixture = TestBed.createComponent(DependabotImpactComponent);
    fixture.componentRef.setInput('drawer', drawerVm);
    const treeSpy = vi.fn();
    fixture.componentInstance.selectTreeNode.subscribe(treeSpy);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.pf-rv-head')?.textContent).toContain('Score 46/100');
    expect(el.querySelectorAll('.pf-tree-node').length).toBeGreaterThan(0);

    const firstNode = el.querySelector('.pf-tree-node') as HTMLButtonElement;
    firstNode.click();
    expect(treeSpy).toHaveBeenCalledWith(0);
  });
});
