import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { provideAppGateways } from '../../app.config';
import { WorkspaceShellComponent } from './workspace-shell.component';

describe('WorkspaceShellComponent (Hive-Inspired UI & Perspectives)', () => {
  it('renders Sidebar, Repo Seats Grid, and switches to CODEOWNERS Inbox, Dependabot Radar, and Timeline', async () => {
    await TestBed.configureTestingModule({
      imports: [WorkspaceShellComponent],
      providers: [...provideAppGateways()],
    }).compileComponents();

    const fixture = TestBed.createComponent(WorkspaceShellComponent);
    fixture.detectChanges();
    const el: HTMLElement = fixture.nativeElement;

    // Default view is Seats de Repositórios
    expect(el.querySelector('.pf-title')?.textContent).toContain('Seats de Repositórios');
    expect(el.querySelectorAll('.pf-seat').length).toBe(4);

    // Switch to Minha Vez (CODEOWNERS)
    const navButtons = Array.from(el.querySelectorAll<HTMLButtonElement>('.pf-nav'));
    navButtons[0].click();
    fixture.detectChanges();
    expect(el.querySelector('.pf-title')?.textContent).toContain('Minha Vez (CODEOWNERS)');
    expect(el.textContent).toContain('feat(seats): add real-time CI status pulse to repo tiles');

    // Open PR #142 in inspection drawer
    const firstCard = el.querySelector<HTMLButtonElement>('.pf-card')!;
    firstCard.click();
    fixture.detectChanges();
    const drawer = el.querySelector('.pf-drawer')!;
    expect(drawer).toBeTruthy();
    expect(drawer.textContent).toContain('arvore/hive-web #142');
    expect(drawer.textContent).toContain('Você foi acionado via regra /src/app/');

    // Switch to Radar Dependabot and inspect PR #233 (7 blast files)
    navButtons[1].click();
    fixture.detectChanges();
    expect(el.querySelector('.pf-title')?.textContent).toContain('Radar Dependabot');
    expect(el.textContent).toContain('4 PRs de Baixo Risco prontos para mesclagem segura');

    fixture.componentInstance.store.openPr(233);
    fixture.detectChanges();
    expect(el.querySelector('.pf-rv-head')?.textContent).toContain('Médio Risco (Score 46/100)');
    expect(el.textContent).toContain('Usa API que quebra');

    // Switch to Linha do Tempo
    navButtons[3].click();
    fixture.detectChanges();
    expect(el.querySelector('.pf-title')?.textContent).toContain('Linha do Tempo');
  });
});
