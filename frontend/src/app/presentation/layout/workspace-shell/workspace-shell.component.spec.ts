import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { WorkspaceShellComponent } from './workspace-shell.component';

describe('WorkspaceShellComponent', () => {
  it('composes sidebar-nav, switches feature views, and docks inspection-drawer when a PR is selected', () => {
    TestBed.configureTestingModule({
      imports: [WorkspaceShellComponent],
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

    const fixture = TestBed.createComponent(WorkspaceShellComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('app-sidebar-nav')).toBeTruthy();
    expect(el.querySelector('app-seats-view')).toBeTruthy();
    expect(el.querySelector('app-inspection-drawer')).toBeNull();

    // Switch to Minha Vez (inbox)
    fixture.componentInstance.store.setPerspective('inbox');
    fixture.detectChanges();
    expect(el.querySelector('app-codeowners-inbox-view')).toBeTruthy();

    // Open PR #142 in inspection drawer
    fixture.componentInstance.store.openPr(142);
    fixture.detectChanges();
    expect(el.querySelector('app-inspection-drawer')).toBeTruthy();

    // Switch to Dependabot Radar
    fixture.componentInstance.store.setPerspective('radar');
    fixture.detectChanges();
    expect(el.querySelector('app-dependabot-radar-view')).toBeTruthy();

    // Switch to Timeline
    fixture.componentInstance.store.setPerspective('timeline');
    fixture.detectChanges();
    expect(el.querySelector('app-timeline-view')).toBeTruthy();
  });
});
