import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { App } from './app';
import { provideAppGateways } from './app.config';
import { AuthStore } from './application/stores/auth.store';

describe('App', () => {
  it('creates the app, renders the login view initially, and opens PR Follower workspace upon sign-in', async () => {
    await TestBed.configureTestingModule({
      imports: [App],
      providers: [...provideAppGateways()],
    }).compileComponents();

    const authStore = TestBed.inject(AuthStore);
    await authStore.logout();

    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    const compiled = fixture.nativeElement as HTMLElement;
    expect(compiled.querySelector('.pf-login-card')).toBeTruthy();

    await authStore.login({ login: 'murilo', team: '@arvore/frontend' }, 5);
    fixture.detectChanges();
    expect(compiled.querySelector('.pf-brand')?.textContent).toContain('PR Follower');
    expect(compiled.querySelector('.pf-title')?.textContent).toContain('Seats de Repositórios');
  });
});
