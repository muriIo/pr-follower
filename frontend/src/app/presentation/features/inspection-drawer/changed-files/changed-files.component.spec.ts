import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { ChangedFileCardViewModel } from '../../../../application/stores/workspace.store';
import { TRANSLATION_GATEWAY } from '../../../../domain/gateways/workspace.gateways';
import { ChangedFilesComponent } from './changed-files.component';

const card = (index: number, path: string, expanded: boolean): ChangedFileCardViewModel => ({
  index,
  path,
  deltaAdd: '+2',
  deltaDel: '-1',
  expanded,
  owned: false,
  hasDiff: true,
  lines: [{ cls: 'is-add', n: '1', tokens: [{ s: '+ new line', cls: '' }] }],
});

function render(files: readonly ChangedFileCardViewModel[], state: 'loading' | 'ready' | 'error' = 'ready') {
  TestBed.configureTestingModule({
    imports: [ChangedFilesComponent],
    providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
  });
  const fixture = TestBed.createComponent(ChangedFilesComponent);
  fixture.componentRef.setInput('files', files);
  fixture.componentRef.setInput('state', state);
  const toggled = vi.fn();
  fixture.componentInstance.toggleFile.subscribe(toggled);
  fixture.detectChanges();
  return { el: fixture.nativeElement as HTMLElement, toggled };
}

describe('ChangedFilesComponent', () => {
  it('shows one collapsible card per changed file, expanding only the open ones', () => {
    const { el, toggled } = render([card(0, 'src/a.ts', true), card(1, 'src/b.ts', false)]);

    const heads = Array.from(el.querySelectorAll('.pf-blast-head')) as HTMLButtonElement[];
    expect(heads.map((h) => h.querySelector('.pf-changed-path')?.textContent?.trim())).toEqual(['src/a.ts', 'src/b.ts']);
    expect(heads.map((h) => h.getAttribute('aria-expanded'))).toEqual(['true', 'false']);
    expect(el.querySelectorAll('app-diff-viewer').length).toBe(1);
    expect(el.textContent).toContain('+2');

    heads[1].click();
    expect(toggled).toHaveBeenCalledWith(1);
  });

  it('says the PR changed no files instead of inventing one', () => {
    const { el } = render([]);
    expect(el.querySelector('.pf-blast-head')).toBeNull();
    expect(el.querySelector('.pf-empty-note')?.textContent).toContain('Este PR não alterou nenhum arquivo.');
  });

  it('tells when a file has no diff to show', () => {
    const { el } = render([{ ...card(0, 'assets/logo.png', true), hasDiff: false, lines: [] }]);
    expect(el.querySelector('app-diff-viewer')).toBeNull();
    expect(el.querySelector('.pf-changed-nodiff')?.textContent).toContain('Sem diff disponível para este arquivo.');
  });

  it('marks the files the user owns', () => {
    const { el } = render([{ ...card(0, 'src/app/seat.ts', true), owned: true }, card(1, 'README.md', false)]);
    const heads = Array.from(el.querySelectorAll('.pf-blast-head'));
    expect(heads[0].querySelector('.pf-changed-owned')?.textContent).toContain('Seu');
    expect(heads[1].querySelector('.pf-changed-owned')).toBeNull();
  });

  it('shows loading and error states instead of an empty list', () => {
    expect(render([], 'loading').el.querySelector('.pf-empty-note')?.textContent).toContain('Carregando arquivos do GitHub...');
    TestBed.resetTestingModule();
    expect(render([], 'error').el.querySelector('.pf-empty-note')?.textContent).toContain('Não foi possível carregar os arquivos do GitHub.');
  });
});
