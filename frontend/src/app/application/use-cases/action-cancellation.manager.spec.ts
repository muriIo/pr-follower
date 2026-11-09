import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { ActionCancellationManager } from './action-cancellation.manager';

describe('ActionCancellationManager (Cooperative AbortSignal Manager)', () => {
  it('tracks running state, completes steps, and clears transient note', async () => {
    TestBed.configureTestingModule({ providers: [ActionCancellationManager] });
    const manager = TestBed.inject(ActionCancellationManager);

    let stepExecuted = false;
    const promise = manager.run(
      'merge:142',
      [
        {
          label: 'Aprovando PR...',
          pct: 50,
          execute: async ({ signal, operationId }) => {
            expect(signal.aborted).toBe(false);
            expect(operationId).toContain('op-merge:142');
            await new Promise((r) => setTimeout(r, 15));
            stepExecuted = true;
          },
        },
      ],
      () => 'Aprovado com sucesso.',
      () => 'Cancelado.',
    );

    expect(manager.getView(undefined, 'merge:142').running).toBe(true);
    expect(manager.getView(undefined, 'merge:142').label).toBe('Aprovando PR...');

    await promise;
    expect(stepExecuted).toBe(true);
    expect(manager.getView(undefined, 'merge:142').running).toBe(false);
    expect(manager.getView(undefined, 'merge:142').note).toBe('Aprovado com sucesso.');
  });

  it('aborts in-flight step immediately when cancel(key) is invoked', async () => {
    const abortedOpIds: string[] = [];
    TestBed.configureTestingModule({ providers: [ActionCancellationManager] });
    const manager = TestBed.inject(ActionCancellationManager);
    manager.registerAbortCallback((opId) => abortedOpIds.push(opId));

    const secondStepSpy = vi.fn();
    const promise = manager.run(
      'batch',
      [
        {
          label: 'Mesclando 1 de 2...',
          pct: 50,
          execute: ({ signal }) =>
            new Promise<void>((resolve, reject) => {
              const t = setTimeout(resolve, 500);
              signal.addEventListener(
                'abort',
                () => {
                  clearTimeout(t);
                  reject(new DOMException('Aborted', 'AbortError'));
                },
                { once: true },
              );
            }),
        },
        {
          label: 'Mesclando 2 de 2...',
          pct: 100,
          execute: async () => {
            secondStepSpy();
          },
        },
      ],
      () => 'Concluído',
      (stepIdx) => `Lote cancelado no passo ${stepIdx}.`,
    );

    expect(manager.getView(undefined, 'batch').running).toBe(true);
    manager.cancel('batch');
    await promise;

    expect(secondStepSpy).not.toHaveBeenCalled();
    expect(abortedOpIds.length).toBe(1);
    expect(manager.getView(undefined, 'batch').running).toBe(false);
    expect(manager.getView(undefined, 'batch').note).toBe('Lote cancelado no passo 0.');
  });
});
