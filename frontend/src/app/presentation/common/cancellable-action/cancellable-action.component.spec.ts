import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { ActionViewModel } from '../../../application/use-cases/action-cancellation.manager';
import { TRANSLATION_GATEWAY } from '../../../domain/gateways/workspace.gateways';
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
      dismissible: false,
      cancel: cancelSpy,
      dismiss: vi.fn(),
    };

    TestBed.configureTestingModule({
      imports: [CancellableActionComponent],
      providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
    });
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

  it('shows a failure beside the controls, as an alert the user dismisses (ADR-0013)', () => {
    const dismiss = vi.fn();
    const failed: ActionViewModel = {
      idle: true,
      running: false,
      hasNote: true,
      label: '',
      pct: 0,
      note: 'O GitHub não permitiu. Required status check "ci" is failing.',
      noteCls: 'pf-note pf-note-err',
      dismissible: true,
      cancel: vi.fn(),
      dismiss,
    };
    TestBed.configureTestingModule({
      imports: [CancellableActionComponent],
      providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
    });
    const fixture = TestBed.createComponent(CancellableActionComponent);
    fixture.componentRef.setInput('action', failed);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    const alert = el.querySelector('[role="alert"]')!;
    expect(alert.textContent).toContain('Required status check "ci" is failing.');
    (alert.querySelector('button') as HTMLButtonElement).click();
    expect(dismiss).toHaveBeenCalledTimes(1);
  });
});
