import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { ActionViewModel } from '../../../../application/use-cases/action-cancellation.manager';
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
      cancel: () => undefined,
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
});
