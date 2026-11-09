import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it } from 'vitest';
import { SseConnectionState } from '../../domain/models/workspace.models';
import { SseLiveEventGateway } from './sse-live-event.gateway';

class FakeEventSource {
  static lastInstance: FakeEventSource | null = null;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  private readonly listeners = new Map<string, ((ev: MessageEvent) => void)[]>();

  constructor(readonly url: string) {
    FakeEventSource.lastInstance = this;
  }

  addEventListener(type: string, listener: (ev: MessageEvent) => void): void {
    const list = this.listeners.get(type) ?? [];
    list.push(listener);
    this.listeners.set(type, list);
  }

  emit(type: string, data: unknown): void {
    const list = this.listeners.get(type) ?? [];
    const ev = { data: JSON.stringify(data) } as MessageEvent;
    list.forEach((fn) => fn(ev));
  }

  close(): void {
    this.closed = true;
  }
}

describe('SseLiveEventGateway', () => {
  const originalEventSource = (globalThis as { EventSource?: unknown }).EventSource;

  afterEach(() => {
    (globalThis as { EventSource?: unknown }).EventSource = originalEventSource;
    FakeEventSource.lastInstance = null;
  });

  it('connects to /api/v1/events/stream via EventSource, parses SSE events, and closes on disconnect', () => {
    (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;

    TestBed.configureTestingModule({ providers: [SseLiveEventGateway] });
    const gateway = TestBed.inject(SseLiveEventGateway);

    const states: SseConnectionState[] = [];
    const events: string[] = [];

    const disconnect = gateway.connect(
      (ev) => events.push(ev.text),
      (state) => states.push(state),
    );

    const es = FakeEventSource.lastInstance!;
    expect(es.url).toBe('/api/v1/events/stream');

    es.onopen?.();
    expect(states).toContain('conectado');

    es.emit('pr.approved', {
      id: 'ev-10',
      type: 'pr.approved',
      repository_name: 'arvore/hive-web',
      pr_number: 142,
      summary: 'Approved PR #142',
      occurred_at: '2026-11-09T19:00:00Z',
    });
    expect(events).toEqual(['Approved PR #142']);

    es.onerror?.();
    expect(states).toContain('reconectando');

    disconnect();
    expect(es.closed).toBe(true);
  });
});
