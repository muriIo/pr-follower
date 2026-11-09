import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { App } from './app';
import { provideAppGateways } from './app.config';

describe('App', () => {
  it('creates the app and renders PR Follower WorkspaceShellComponent', async () => {
    await TestBed.configureTestingModule({
      imports: [App],
      providers: [...provideAppGateways()],
    }).compileComponents();

    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    const compiled = fixture.nativeElement as HTMLElement;
    expect(compiled.querySelector('.pf-brand')?.textContent).toContain('PR Follower');
    expect(compiled.querySelector('.pf-title')?.textContent).toContain('Seats de Repositórios');
  });
});
