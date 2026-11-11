import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { InboxCardViewModel } from '../../../../application/stores/workspace.store';
import { InboxPrCardComponent } from './inbox-pr-card.component';

describe('InboxPrCardComponent', () => {
  it('renders CODEOWNERS inbox card and emits selected PR id on click', () => {
    const cardVm: InboxCardViewModel = {
      id: 9142,
      number: 142,
      repo: 'arvore/hive-web',
      title: 'feat(seats): add real-time CI status pulse to repo tiles',
      sel: 'is-sel',
      authorLabel: '@bianca-dev',
      ciLabel: 'CI Verde',
      ciTextCls: 'pf-ci-stable',
      hasPill: true,
      ownPill: '🎯 Toca apenas 1 arquivo seu',
      hasClaim: true,
      claim: 'Assumido por @murilo',
      badge: '✅ Você aprovou',
      badgeTip: '',
    };

    TestBed.configureTestingModule({ imports: [InboxPrCardComponent] });
    const fixture = TestBed.createComponent(InboxPrCardComponent);
    fixture.componentRef.setInput('card', cardVm);
    const selectSpy = vi.fn();
    fixture.componentInstance.selected.subscribe(selectSpy);
    fixture.detectChanges();

    const btn = fixture.nativeElement.querySelector('button.pf-card') as HTMLButtonElement;
    expect(btn).toBeTruthy();
    expect(btn.textContent).toContain('arvore/hive-web #142');
    expect(btn.textContent).toContain('🎯 Toca apenas 1 arquivo seu');

    btn.click();
    expect(selectSpy).toHaveBeenCalledWith(9142);
  });
});
