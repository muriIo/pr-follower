import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './app';
import { provideAppGateways } from './app.config';
import { AuthStore } from './application/stores/auth.store';

describe('App', () => {
  afterEach(() => vi.restoreAllMocks());

  it('renders the GitHub sign-in when signed out and the PR Follower workspace for a session user', async () => {
    let signedIn = false;
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/api/v1/auth/me')) {
        return signedIn
          ? new Response(JSON.stringify({ user: { id: 7, login: 'octocat', teams: [] } }), { status: 200 })
          : new Response('{"error":"unauthorized"}', { status: 401 });
      }
      return new Response('{}', { status: 503 });
    });

    await TestBed.configureTestingModule({
      imports: [App],
      providers: [...provideAppGateways()],
    }).compileComponents();

    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    const authStore = TestBed.inject(AuthStore);
    await authStore.restore();
    fixture.detectChanges();
    const compiled = fixture.nativeElement as HTMLElement;
    expect(compiled.querySelector('.pf-login-card')).toBeTruthy();

    signedIn = true;
    await authStore.restore();
    fixture.detectChanges();
    expect(compiled.querySelector('.pf-brand')?.textContent).toContain('PR Follower');
    expect(compiled.querySelector('.pf-title')?.textContent).toContain('Seats de Repositórios');
  });
});
