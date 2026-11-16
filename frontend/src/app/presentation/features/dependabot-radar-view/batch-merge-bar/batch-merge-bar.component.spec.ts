import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { ActionViewModel } from '../../../../application/use-cases/action-cancellation.manager';
import { TRANSLATION_GATEWAY } from '../../../../domain/gateways/workspace.gateways';
import { BatchMergeBarComponent, SafeBatchSummaryViewModel } from './batch-merge-bar.component';

describe('BatchMergeBarComponent', () => {
  it('renders batch merge banner and emits batchApprove and batchMerge events', () => {
    const summary: SafeBatchSummaryViewModel = {
      has: true,
      title: '🟢 4 PRs de Baixo Risco prontos para mesclagem segura',
      canApprove: true,
      approveLabel: 'Aprovar Todos os 4',
      mergeDis: 'false',
      mergeCls: 'pf-btn-primary',
      mergeLabel: 'Mesclar Todos os 4',
    };
    const idleAction: ActionViewModel = {
      idle: true,
      running: false,
      hasNote: false,
      label: '',
      pct: 0,
      note: '',
      noteCls: 'pf-note',
      dismissible: false,
      cancel: () => undefined,
      dismiss: () => undefined,
    };

    TestBed.configureTestingModule({ imports: [BatchMergeBarComponent] });
    const fixture = TestBed.createComponent(BatchMergeBarComponent);
    fixture.componentRef.setInput('summary', summary);
    fixture.componentRef.setInput('batchAction', idleAction);

    const approveSpy = vi.fn();
    const mergeSpy = vi.fn();
    fixture.componentInstance.batchApprove.subscribe(approveSpy);
    fixture.componentInstance.batchMerge.subscribe(mergeSpy);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('🟢 4 PRs de Baixo Risco');

    const btns = el.querySelectorAll('.pf-actions button') as NodeListOf<HTMLButtonElement>;
    btns[0].click();
    btns[1].click();
    expect(approveSpy).toHaveBeenCalledTimes(1);
    expect(mergeSpy).toHaveBeenCalledTimes(1);
  });

  it('keeps a stopped batch on screen as an alert the user dismisses (ADR-0013)', () => {
    const dismiss = vi.fn();
    TestBed.configureTestingModule({
      imports: [BatchMergeBarComponent],
      providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
    });
    const fixture = TestBed.createComponent(BatchMergeBarComponent);
    fixture.componentRef.setInput('summary', { has: false, title: '', canApprove: false, approveLabel: '', mergeDis: 'true', mergeCls: '', mergeLabel: '' });
    fixture.componentRef.setInput('batchAction', {
      idle: true, running: false, hasNote: true, label: '', pct: 0,
      note: 'Lote parado: 1 de 3 mesclados. O PR #102 falhou.', noteCls: 'pf-note pf-note-err',
      dismissible: true, cancel: () => undefined, dismiss,
    } satisfies ActionViewModel);
    fixture.detectChanges();

    const alert = (fixture.nativeElement as HTMLElement).querySelector('[role="alert"]')!;
    expect(alert.textContent).toContain('O PR #102 falhou.');
    (alert.querySelector('button') as HTMLButtonElement).click();
    expect(dismiss).toHaveBeenCalledTimes(1);
  });
});
