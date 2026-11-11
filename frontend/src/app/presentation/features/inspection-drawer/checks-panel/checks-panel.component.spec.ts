import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { DrawerChecksViewModel } from '../../../../application/stores/workspace.store';
import { TRANSLATION_GATEWAY } from '../../../../domain/gateways/workspace.gateways';
import { ChecksPanelComponent } from './checks-panel.component';

const item = (name: string, state: 'failure' | 'pending' | 'success' | 'skipped', detail = '', url: string | null = null) => ({ name, state, detail, url });

const checks: DrawerChecksViewModel = {
  has: true,
  tone: 'fail',
  title: 'Alguns checks não foram bem-sucedidos',
  subtitle: '2 falhando, 3 bem-sucedidos',
  ring: 'conic-gradient(#f43f5e 0 40%, #10b981 40% 100%)',
  groups: [
    { key: 'failure', label: '2 checks falhando', open: true, items: [item('api-iam', 'failure', 'Checks falharam', 'https://ci.example.com/iam'), item('api-learning', 'failure')] },
    { key: 'success', label: '3 checks bem-sucedidos', open: false, items: [item('web', 'success', 'Sem mudanças'), item('tenant', 'success'), item('backoffice', 'success')] },
  ],
};

function render(vm: DrawerChecksViewModel) {
  TestBed.configureTestingModule({
    imports: [ChecksPanelComponent],
    providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
  });
  const fixture = TestBed.createComponent(ChecksPanelComponent);
  fixture.componentRef.setInput('checks', vm);
  fixture.detectChanges();
  return { fixture, el: fixture.nativeElement as HTMLElement };
}

describe('ChecksPanelComponent (GitHub-style checks panel)', () => {
  it('leads with the verdict and the counts, then lists the failing checks open and the successful ones collapsed', () => {
    const { el } = render(checks);

    expect(el.querySelector('.pf-checks-title')?.textContent).toContain('Alguns checks não foram bem-sucedidos');
    expect(el.querySelector('.pf-checks-sub')?.textContent).toContain('2 falhando, 3 bem-sucedidos');
    expect(Array.from(el.querySelectorAll('.pf-checks-group-head')).map((b) => b.textContent!.trim())).toEqual(['2 checks falhando', '3 checks bem-sucedidos']);

    expect(Array.from(el.querySelectorAll('.pf-check-name')).map((n) => n.textContent!.trim())).toEqual(['api-iam', 'api-learning']);
    expect(el.querySelector('.pf-check')!.textContent).toContain('Checks falharam');
  });

  it('expands and collapses a group on click', () => {
    const { fixture, el } = render(checks);
    const successHead = el.querySelectorAll('.pf-checks-group-head')[1] as HTMLButtonElement;
    expect(successHead.getAttribute('aria-expanded')).toBe('false');

    successHead.click();
    fixture.detectChanges();
    expect(successHead.getAttribute('aria-expanded')).toBe('true');
    expect(el.querySelectorAll('.pf-check-name')).toHaveLength(5);

    successHead.click();
    fixture.detectChanges();
    expect(el.querySelectorAll('.pf-check-name')).toHaveLength(2);
  });

  it('links a check to where it reports, safely, and leaves the others as plain text', () => {
    const { el } = render(checks);
    const link = el.querySelector('a.pf-check-name') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('https://ci.example.com/iam');
    expect(link.getAttribute('rel')).toContain('noopener');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(el.querySelectorAll('a.pf-check-name')).toHaveLength(1);
  });

  it('renders nothing when the commit reported no checks', () => {
    const { el } = render({ ...checks, has: false, groups: [] });
    expect(el.querySelector('.pf-checks')).toBeNull();
  });
});
