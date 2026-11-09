import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { SidebarNavComponent } from './sidebar-nav.component';

describe('SidebarNavComponent', () => {
  it('renders brand, SSE live connection status, perspective buttons, and switches locale', async () => {
    TestBed.configureTestingModule({
      imports: [SidebarNavComponent],
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
          useValue: { loadDictionary: async () => ({ 'app.brand': 'PR Follower' }) },
        },
      ],
    });

    const fixture = TestBed.createComponent(SidebarNavComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    const navButtons = el.querySelectorAll('button.pf-nav') as NodeListOf<HTMLButtonElement>;
    expect(navButtons.length).toBe(4);

    navButtons[0].click();
    fixture.detectChanges();
    expect(fixture.componentInstance.store.view()).toBe('inbox');

    const localeBtn = el.querySelector('.pf-user-tags button.pf-badge') as HTMLButtonElement;
    expect(localeBtn.textContent?.trim()).toBe('pt-BR');
    localeBtn.click();
    await new Promise((r) => setTimeout(r, 5));
    fixture.detectChanges();
    expect(fixture.componentInstance.localeStore.locale()).toBe('en-US');
  });
});
