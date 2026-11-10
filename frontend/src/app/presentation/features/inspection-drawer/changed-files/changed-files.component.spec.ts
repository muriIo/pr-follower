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
  hasDiff: true,
  lines: [{ cls: 'is-add', n: '1', tokens: [{ s: '+ new line', cls: '' }] }],
});

function render(files: readonly ChangedFileCardViewModel[]) {
  TestBed.configureTestingModule({
    imports: [ChangedFilesComponent],
    providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
  });
  const fixture = TestBed.createComponent(ChangedFilesComponent);
  fixture.componentRef.setInput('files', files);
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

  it('says the preview is not available instead of inventing a file', () => {
    const { el } = render([]);
    expect(el.querySelector('.pf-blast-head')).toBeNull();
    expect(el.querySelector('.pf-empty-note')?.textContent).toContain('A prévia dos arquivos alterados ainda não está disponível.');
  });

  it('tells when a file has no diff to show', () => {
    const { el } = render([{ ...card(0, 'assets/logo.png', true), hasDiff: false, lines: [] }]);
    expect(el.querySelector('app-diff-viewer')).toBeNull();
    expect(el.querySelector('.pf-changed-nodiff')?.textContent).toContain('Sem diff disponível para este arquivo.');
  });
});
