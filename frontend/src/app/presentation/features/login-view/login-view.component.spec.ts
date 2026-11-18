import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { AUTH_GATEWAY, TRANSLATION_GATEWAY } from '../../../domain/gateways/workspace.gateways';
import { AuthStore } from '../../../application/stores/auth.store';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { LoginViewComponent } from './login-view.component';

function setup(authError: string | null = null) {
  const gateway = {
    fetchCurrentUser: async () => null,
    startSignIn: vi.fn(),
    consumeAuthError: () => authError,
    logout: async () => undefined,
    verifySession: async () => 'expired' as const,
  };
  TestBed.configureTestingModule({
    imports: [LoginViewComponent],
    providers: [
      AuthStore,
      { provide: WorkspaceStore, useValue: { startLiveSession: vi.fn(), stopLiveSession: vi.fn(), closeDrawer: vi.fn() } },
      { provide: AUTH_GATEWAY, useValue: gateway },
      { provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } },
    ],
  });
  return { gateway, authStore: TestBed.inject(AuthStore) };
}

describe('LoginViewComponent (GitHub-only sign-in)', () => {
  it('offers a single "sign in with GitHub" action and no credential fields', async () => {
    const { gateway, authStore } = setup();
    await authStore.restore();

    const fixture = TestBed.createComponent(LoginViewComponent);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;

    expect(el.querySelector('.pf-login-card')).toBeTruthy();
    expect(el.querySelectorAll('input').length).toBe(0);
    expect(el.querySelector('.pf-login-error')).toBeNull();

    const button = el.querySelector('#pf-login-github') as HTMLButtonElement;
    expect(button).toBeTruthy();
    button.click();
    fixture.detectChanges();

    expect(gateway.startSignIn).toHaveBeenCalledTimes(1);
    expect((el.querySelector('#pf-login-github') as HTMLButtonElement).disabled).toBe(true);
  });

  it('explains why the previous sign-in attempt failed', async () => {
    const { authStore } = setup('access_denied');
    await authStore.restore();

    const fixture = TestBed.createComponent(LoginViewComponent);
    fixture.detectChanges();
    const error = (fixture.nativeElement as HTMLElement).querySelector('.pf-login-error');
    expect(error?.textContent).toContain('Você cancelou a autorização no GitHub.');
  });

  it('says a refused account is not a member of the allowed GitHub organizations (ADR-0015)', async () => {
    const { authStore } = setup('not_org_member');
    await authStore.restore();

    const fixture = TestBed.createComponent(LoginViewComponent);
    fixture.detectChanges();
    const error = (fixture.nativeElement as HTMLElement).querySelector('.pf-login-error');
    expect(error?.textContent).toContain('não é membro');
  });

  it('says membership could not be verified, without claiming the user is not a member', async () => {
    const { authStore } = setup('org_membership_unverified');
    await authStore.restore();

    const fixture = TestBed.createComponent(LoginViewComponent);
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).querySelector('.pf-login-error')?.textContent ?? '';
    expect(text).toContain('Não foi possível verificar');
    expect(text).not.toContain('não é membro');
  });

  it('tells the user their session expired when the app sent them back here', async () => {
    const { authStore } = setup();
    await authStore.restore();
    authStore.authError.set('session_expired');

    const fixture = TestBed.createComponent(LoginViewComponent);
    fixture.detectChanges();
    const error = (fixture.nativeElement as HTMLElement).querySelector('.pf-login-error');
    expect(error?.textContent).toContain('Sua sessão expirou. Entre de novo.');
  });
});
