import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { SeatsViewComponent } from './seats-view.component';
import { loadWorkspaceFixture } from '../../../testing/workspace-fixtures';
import { WorkspaceStore } from '../../../application/stores/workspace.store';

describe('SeatsViewComponent', () => {
  it('renders seat filter pills, seat cards, and filters by turn', () => {
    TestBed.configureTestingModule({
      imports: [SeatsViewComponent],
      providers: [
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

    loadWorkspaceFixture(TestBed.inject(WorkspaceStore));

    const fixture = TestBed.createComponent(SeatsViewComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelectorAll('app-seat-card').length).toBe(4);

    const filterBtns = el.querySelectorAll('.pf-filter') as NodeListOf<HTMLButtonElement>;
    expect(filterBtns.length).toBe(3);
    filterBtns[1].click();
    fixture.detectChanges();
    expect(el.querySelectorAll('app-seat-card').length).toBe(2);

    // "+ Importar repositório" opens the Vercel-style import screen
    (el.querySelector('#pf-import-open') as HTMLButtonElement).click();
    expect(fixture.componentInstance.store.view()).toBe('import');
  });
});
