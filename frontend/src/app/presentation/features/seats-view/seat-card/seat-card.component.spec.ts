import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { SeatCardViewModel } from '../../../../application/stores/workspace.store';
import { SeatCardComponent } from './seat-card.component';

describe('SeatCardComponent', () => {
  it('renders seat repository header, KPIs, and PR stream rows, and emits openRadar when Dependabot pill is clicked', () => {
    const openSpy = vi.fn();
    const quickSpy = vi.fn();
    const seatVm: SeatCardViewModel = {
      name: 'arvore/hive-web',
      branch: 'main',
      cls: '',
      ciDot: 'pf-dot pf-dot-ok',
      ciCls: 'pf-ci-stable',
      ciLabel: 'CI Estável',
      ciTip: '',
      turn: 1,
      turnCls: 'pf-turn',
      deps: 3,
      depCls: 'pf-badge-baixo',
      openLabel: '4 PRs Abertos',
      isEmpty: false,
      accessKey: null,
      noCodeowners: false,
      stream: [
        {
          id: 9142,
          number: 142,
          title: 'feat(seats): add real-time CI status pulse',
          sel: '',
          dim: '',
          showTurn: true,
          hasOwn: true,
          ownLabel: '1 arquivo seu',
          showRisk: false,
          vCls: 'pf-badge-baixo',
          riskTip: '',
          riskLabel: 'Baixo Risco',
          isApproved: false,
          approvedTip: '',
          approvedLabel: '',
          hasQuick: true,
          act: {
            idle: true,
            running: false,
            hasNote: false,
            label: '',
            pct: 0,
            note: '',
            noteCls: 'pf-note',
            cancel: () => undefined,
          },
          quickTip: '',
          quickLabel: 'Aprovar',
          open: openSpy,
          quick: quickSpy,
        },
      ],
    };

    TestBed.configureTestingModule({ imports: [SeatCardComponent] });
    const fixture = TestBed.createComponent(SeatCardComponent);
    fixture.componentRef.setInput('seat', seatVm);
    const radarSpy = vi.fn();
    fixture.componentInstance.openRadar.subscribe(radarSpy);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.pf-seat-name')?.textContent).toBe('arvore/hive-web');
    expect(el.textContent).toContain('#142');

    const rowBtn = el.querySelector('.pf-srow-open') as HTMLButtonElement;
    rowBtn.click();
    expect(openSpy).toHaveBeenCalledTimes(1);

    const depBtn = el.querySelector('.pf-kpis button.pf-badge') as HTMLButtonElement;
    depBtn.click();
    expect(radarSpy).toHaveBeenCalledTimes(1);
    expect(el.querySelector('.pf-seat-access')).toBeNull();

    // A repository the GitHub App no longer reaches is flagged with a way to fix it.
    fixture.componentRef.setInput('seat', { ...seatVm, accessKey: 'seats.access.removed' });
    const fixSpy = vi.fn();
    fixture.componentInstance.fixAccess.subscribe(fixSpy);
    fixture.detectChanges();
    expect(el.querySelector('.pf-seat-access')).toBeTruthy();
    (el.querySelector('.pf-seat-access-fix') as HTMLButtonElement).click();
    expect(fixSpy).toHaveBeenCalledTimes(1);

    // A repository without a CODEOWNERS file says so instead of implying pending reviews.
    expect(el.querySelector('.pf-seat-no-codeowners')).toBeNull();
    fixture.componentRef.setInput('seat', { ...seatVm, noCodeowners: true });
    fixture.detectChanges();
    expect(el.querySelector('.pf-seat-no-codeowners')?.textContent?.trim()).toBe('Sem CODEOWNERS');
  });
});
