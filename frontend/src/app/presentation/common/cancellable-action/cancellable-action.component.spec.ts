import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { ActionViewModel } from '../../../application/use-cases/action-cancellation.manager';
import { CancellableActionComponent } from './cancellable-action.component';

describe('CancellableActionComponent', () => {
  it('renders running state and invokes cancel callback when cancel button is clicked', () => {
    const cancelSpy = vi.fn();
    const runningAction: ActionViewModel = {
      idle: false,
      running: true,
      hasNote: false,
      label: 'Mesclando via squash...',
      pct: 50,
      note: '',
      noteCls: 'pf-note',
      cancel: cancelSpy,
    };

    TestBed.configureTestingModule({ imports: [CancellableActionComponent] });
    const fixture = TestBed.createComponent(CancellableActionComponent);
    fixture.componentRef.setInput('action', runningAction);
    fixture.componentRef.setInput('cancelLabel', 'Cancelar (Esc)');
    fixture.componentRef.setInput('mode', 'drawer');
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Mesclando via squash...');

    const cancelBtn = el.querySelector('button.pf-btn-danger') as HTMLButtonElement;
    expect(cancelBtn).toBeTruthy();
    cancelBtn.click();
    expect(cancelSpy).toHaveBeenCalledTimes(1);
  });
});
