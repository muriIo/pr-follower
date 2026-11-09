import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { RadarCardViewModel } from '../../../../application/stores/workspace.store';
import { RadarPrCardComponent } from './radar-pr-card.component';

describe('RadarPrCardComponent', () => {
  it('renders Dependabot radar card and emits selected PR id on click', () => {
    const card: RadarCardViewModel = {
      id: 89,
      repo: 'arvore/checkout-api',
      radarTitle: 'bump axios from 1.7.7 to 2.0.0',
      radarMeta: 'Major • Prod Transitiva via @nestjs/axios',
      metaTip: '',
      ciShort: 'CI verde',
      ciTextCls: 'pf-ci-ok',
      approvedMine: true,
      sel: '',
    };

    TestBed.configureTestingModule({ imports: [RadarPrCardComponent] });
    const fixture = TestBed.createComponent(RadarPrCardComponent);
    fixture.componentRef.setInput('card', card);
    const selectSpy = vi.fn();
    fixture.componentInstance.selected.subscribe(selectSpy);
    fixture.detectChanges();

    const btn = fixture.nativeElement.querySelector('button.pf-card') as HTMLButtonElement;
    expect(btn.textContent).toContain('arvore/checkout-api #89');
    expect(btn.textContent).toContain('bump axios from 1.7.7 to 2.0.0');

    btn.click();
    expect(selectSpy).toHaveBeenCalledWith(89);
  });
});
