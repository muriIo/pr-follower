import { DestroyRef, Injectable, inject, signal } from '@angular/core';
import { ActionRequestOptions } from '../../domain/gateways/workspace.gateways';

export interface CancellableStep {
  readonly label: string;
  readonly pct?: number;
  readonly execute: (options: Required<ActionRequestOptions>) => Promise<void>;
}

export interface ActionStateEntry {
  readonly status: 'running' | 'ok' | 'cancel' | 'error';
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
  /** A failure stays until the user dismisses it (ADR-0013); other notes fade on their own. */
  readonly dismissible: boolean;
  readonly cancel: () => void;
  readonly dismiss: () => void;
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
      // Not running: the controls are back, also beside a note, so a failure can be retried at once.
      idle: !running,
      running,
      hasNote,
      label: entry?.label ?? '',
      pct: entry?.pct ?? 0,
      note: entry?.note ?? '',
      noteCls: entry?.status === 'ok' ? 'pf-note pf-note-ok' : entry?.status === 'error' ? 'pf-note pf-note-err' : 'pf-note',
      dismissible: entry?.status === 'error' && hasNote,
      cancel: () => this.cancel(key),
      dismiss: () => this.dismiss(key),
    };
  }

  async run(
    key: string,
    steps: readonly CancellableStep[],
    onDone: () => string | null,
    onCancelled: (stepIndex: number) => string,
    onFailed?: (err: unknown, stepIndex: number) => string | null,
  ): Promise<void> {
    this.clearTimer(key);
    const controller = new AbortController();
    const operationId = `op-${key}-${Date.now()}`;
    this.controllers.set(key, { controller, operationId });

    let stepIndex = 0;
    try {
      for (; stepIndex < steps.length; stepIndex++) {
        // A step that ignores the signal must not let the next one start after a cancel.
        if (controller.signal.aborted) {
          throw new DOMException('Aborted', 'AbortError');
        }
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
      if (!onFailed) {
        this.finish(key, 'error', null);
        throw err;
      }
      this.finish(key, 'error', onFailed(err, stepIndex));
    }
  }

  /** A running action reports how far it is; ignored once it is over. */
  setProgress(key: string, label: string, pct?: number): void {
    const entry = this.actionsState()[key];
    if (entry?.status !== 'running') {
      return;
    }
    this.setAction(key, { ...entry, label, pct });
  }

  cancel(key: string): void {
    const active = this.controllers.get(key);
    if (active) {
      this.abortCallback?.(active.operationId);
      active.controller.abort();
      return;
    }
    // Nothing is running: there is nothing to report, so just clear the action.
    this.finish(key, 'cancel', null);
  }

  /** Clears a finished action's note (a failure the user has read). */
  dismiss(key: string): void {
    if (this.controllers.has(key)) {
      return; // still running: nothing to dismiss
    }
    this.clearTimer(key);
    this.setAction(key, null);
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

  private finish(key: string, status: 'ok' | 'cancel' | 'error', note: string | null): void {
    this.controllers.delete(key);
    if (!note) {
      this.setAction(key, null);
      return;
    }
    this.setAction(key, { status, note });
    if (status === 'error') {
      return; // a failure the user has not seen must not vanish (ADR-0013)
    }
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
