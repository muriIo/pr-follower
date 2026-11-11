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

describe('ActionCancellationManager without a running action', () => {
  it('clears the action instead of reporting invented results', () => {
    TestBed.configureTestingModule({ providers: [ActionCancellationManager] });
    const manager = TestBed.inject(ActionCancellationManager);
    manager.cancel('batch');
    expect(manager.getView(manager.actionsState(), 'batch').note).toBe('');
    expect(manager.getView(manager.actionsState(), 'batch').idle).toBe(true);
  });
});

describe('ActionCancellationManager failures and cancellation between steps', () => {
  it('shows a failure as an error note instead of throwing into the void', async () => {
    TestBed.configureTestingModule({ providers: [ActionCancellationManager] });
    const manager = TestBed.inject(ActionCancellationManager);

    await manager.run(
      'merge:7',
      [{ label: 'Aprovando...', execute: async () => { throw new Error('boom'); } }],
      () => 'ok',
      () => 'cancelado',
      (err, stepIndex) => `falhou ${stepIndex}: ${(err as Error).message}`,
    );

    const view = manager.getView(undefined, 'merge:7');
    expect(view.running).toBe(false);
    expect(view.note).toBe('falhou 0: boom');
    expect(view.noteCls).toBe('pf-note pf-note-err');
  });

  it('still throws when nobody handles the failure', async () => {
    TestBed.configureTestingModule({ providers: [ActionCancellationManager] });
    const manager = TestBed.inject(ActionCancellationManager);
    await expect(
      manager.run('k', [{ label: 'x', execute: async () => { throw new Error('boom'); } }], () => null, () => 'c'),
    ).rejects.toThrow('boom');
  });

  it('stops before the next step once cancelled, even when the running step ignores the signal', async () => {
    TestBed.configureTestingModule({ providers: [ActionCancellationManager] });
    const manager = TestBed.inject(ActionCancellationManager);
    const second = vi.fn();

    const promise = manager.run(
      'batch',
      [
        { label: '1', execute: async () => undefined },
        { label: '2', execute: async () => second() },
      ],
      () => 'done',
      (idx) => `cancelled at ${idx}`,
    );
    manager.cancel('batch');
    await promise;

    expect(second).not.toHaveBeenCalled();
    expect(manager.getView(undefined, 'batch').note).toBe('cancelled at 1');
  });
});

describe('ActionCancellationManager progress', () => {
  it('lets a running action update its label and bar as it goes', async () => {
    TestBed.configureTestingModule({ providers: [ActionCancellationManager] });
    const manager = TestBed.inject(ActionCancellationManager);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));

    const promise = manager.run('sync', [{ label: 'Sincronizando 10 repositórios...', execute: async () => gate }], () => 'ok', () => 'cancelado');
    manager.setProgress('sync', 'Sincronizando 3 de 10: acme/web...', 30);

    const view = manager.getView(undefined, 'sync');
    expect(view.running).toBe(true);
    expect(view.label).toBe('Sincronizando 3 de 10: acme/web...');
    expect(view.pct).toBe(30);

    release();
    await promise;
    manager.setProgress('sync', 'late', 90); // after it finished: ignored
    expect(manager.getView(undefined, 'sync').label).toBe('');
  });
});
