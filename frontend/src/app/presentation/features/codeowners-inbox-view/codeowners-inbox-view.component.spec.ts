import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { CodeownersInboxViewComponent } from './codeowners-inbox-view.component';
import { loadWorkspaceFixture } from '../../../testing/workspace-fixtures';
import { WorkspaceStore } from '../../../application/stores/workspace.store';

describe('CodeownersInboxViewComponent', () => {
  it('renders active waiting-on-me PRs and toggles muted/gated PRs', () => {
    TestBed.configureTestingModule({
      imports: [CodeownersInboxViewComponent],
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

    const fixture = TestBed.createComponent(CodeownersInboxViewComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelectorAll('app-inbox-pr-card').length).toBe(5);

    const toggleBtn = el.querySelector('button.pf-toggle') as HTMLButtonElement;
    toggleBtn.click();
    fixture.detectChanges();
    expect(el.querySelectorAll('app-inbox-pr-card').length).toBe(2);
  });
});
