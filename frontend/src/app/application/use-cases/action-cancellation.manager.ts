import { DestroyRef, Injectable, inject, signal } from '@angular/core';
import { ActionRequestOptions } from '../../domain/gateways/workspace.gateways';

export interface CancellableStep {
  readonly label: string;
  readonly pct?: number;
  readonly execute: (options: Required<ActionRequestOptions>) => Promise<void>;
}

export interface ActionStateEntry {
  readonly status: 'running' | 'ok' | 'cancel';
  readonly label?: string;
  readonly pct?: number;
  readonly note?: string;
  readonly operationId?: string;
}

export interface ActionViewModel {
  readonly idle: boolean;
  readonly running: boolean;
  readonly hasNote: boolean;
  readonly label: string;
  readonly pct: number;
  readonly note: string;
  readonly noteCls: string;
  readonly cancel: () => void;
}

@Injectable({ providedIn: 'root' })
export class ActionCancellationManager {
  private readonly destroyRef = inject(DestroyRef);
  private readonly controllers = new Map<string, { controller: AbortController; operationId: string }>();
  private readonly noteTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private abortCallback?: (operationId: string) => void;

  readonly actionsState = signal<Record<string, ActionStateEntry>>({});

  constructor() {
    this.destroyRef.onDestroy(() => {
      this.noteTimers.forEach((timer) => clearTimeout(timer));
      this.controllers.forEach(({ controller }) => controller.abort());
      this.controllers.clear();
    });
  }

  registerAbortCallback(cb: (operationId: string) => void): void {
    this.abortCallback = cb;
  }

  getView(stateMap: Record<string, ActionStateEntry> = this.actionsState(), key: string): ActionViewModel {
    const entry = stateMap[key];
    const running = Boolean(entry && entry.status === 'running');
    const hasNote = Boolean(entry && !running && entry.note);
    return {
      idle: !entry,
      running,
      hasNote,
      label: entry?.label ?? '',
      pct: entry?.pct ?? 0,
      note: entry?.note ?? '',
      noteCls: entry?.status === 'ok' ? 'pf-note pf-note-ok' : 'pf-note',
      cancel: () => this.cancel(key),
    };
  }

  async run(
    key: string,
    steps: readonly CancellableStep[],
    onDone: () => string | null,
    onCancelled: (stepIndex: number) => string,
  ): Promise<void> {
    this.clearTimer(key);
    const controller = new AbortController();
    const operationId = `op-${key}-${Date.now()}`;
    this.controllers.set(key, { controller, operationId });

    let stepIndex = 0;
    try {
      for (; stepIndex < steps.length; stepIndex++) {
        const step = steps[stepIndex];
        this.setAction(key, {
          status: 'running',
          label: step.label,
          pct: step.pct,
          operationId,
        });
        await step.execute({ signal: controller.signal, operationId });
      }
      const doneNote = onDone();
      this.finish(key, 'ok', doneNote);
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        this.finish(key, 'cancel', onCancelled(stepIndex));
        return;
      }
      throw err;
    }
  }

  cancel(key: string): void {
    const active = this.controllers.get(key);
    if (active) {
      this.abortCallback?.(active.operationId);
      active.controller.abort();
      return;
    }
    this.finish(
      key,
      'cancel',
      key === 'batch'
        ? 'Lote cancelado. 1 PR mesclado, 3 continuam abertos.'
        : 'Cancelado. Nada foi enviado ao GitHub.',
    );
  }

  private setAction(key: string, entry: ActionStateEntry | null): void {
    this.actionsState.update((current) => {
      const next = { ...current };
      if (entry) {
        next[key] = entry;
      } else {
        delete next[key];
      }
      return next;
    });
  }

  private finish(key: string, status: 'ok' | 'cancel', note: string | null): void {
    this.controllers.delete(key);
    if (!note) {
      this.setAction(key, null);
      return;
    }
    this.setAction(key, { status, note });
    const timer = setTimeout(() => {
      this.noteTimers.delete(key);
      this.setAction(key, null);
    }, 6000);
    this.noteTimers.set(key, timer);
  }

  private clearTimer(key: string): void {
    const existing = this.noteTimers.get(key);
    if (existing) {
      clearTimeout(existing);
      this.noteTimers.delete(key);
    }
  }
}
