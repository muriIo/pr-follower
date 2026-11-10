import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { TimelineViewComponent } from './timeline-view.component';
import { loadWorkspaceFixture } from '../../../testing/workspace-fixtures';
import { WorkspaceStore } from '../../../application/stores/workspace.store';

describe('TimelineViewComponent', () => {
  it('renders live activity groups and toggles timeline pause state', () => {
    TestBed.configureTestingModule({
      imports: [TimelineViewComponent],
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

    const fixture = TestBed.createComponent(TimelineViewComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelectorAll('.pf-event').length).toBeGreaterThan(0);

    const pauseBtn = el.querySelector('.pf-actions button.pf-btn') as HTMLButtonElement;
    expect(fixture.componentInstance.store.pausedTimeline()).toBe(false);
    pauseBtn.click();
    fixture.detectChanges();
    expect(fixture.componentInstance.store.pausedTimeline()).toBe(true);
  });
});
