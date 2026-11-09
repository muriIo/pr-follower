import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { InspectionDrawerComponent } from './inspection-drawer.component';

describe('InspectionDrawerComponent', () => {
  it('renders docked PR inspection drawer with diff viewer and closes when close button is clicked', () => {
    TestBed.configureTestingModule({
      imports: [InspectionDrawerComponent],
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
    store.openPr(142);

    const fixture = TestBed.createComponent(InspectionDrawerComponent);
    fixture.componentRef.setInput('drawer', store.drawer()!);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.pf-drawer-title')?.textContent).toContain('real-time CI status pulse');
    expect(el.querySelector('app-diff-viewer')).toBeTruthy();

    const closeBtn = el.querySelector('#pf-drawer-close') as HTMLButtonElement;
    closeBtn.click();
    expect(store.selectedPrId()).toBeNull();
  });
});
