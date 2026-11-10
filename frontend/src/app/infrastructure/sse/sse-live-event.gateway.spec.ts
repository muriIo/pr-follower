import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SseConnectionState } from '../../domain/models/workspace.models';
import { SseLiveEventGateway } from './sse-live-event.gateway';

class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = FakeEventSource.CONNECTING;
  closed = false;
  private readonly listeners = new Map<string, ((ev: MessageEvent) => void)[]>();

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }

  static get last(): FakeEventSource {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1];
  }

  addEventListener(type: string, listener: (ev: MessageEvent) => void): void {
    const list = this.listeners.get(type) ?? [];
    list.push(listener);
    this.listeners.set(type, list);
  }

  emit(type: string, data: unknown): void {
    (this.listeners.get(type) ?? []).forEach((fn) => fn({ data: JSON.stringify(data) } as MessageEvent));
  }

  open(): void {
    this.readyState = FakeEventSource.OPEN;
    this.onopen?.();
  }

  /** The browser gave up on this connection (non-200 response, e.g. 502 or 401). */
  fail(): void {
    this.readyState = FakeEventSource.CLOSED;
    this.onerror?.();
  }

  /** Network blip: the browser is retrying on its own. */
  blip(): void {
    this.readyState = FakeEventSource.CONNECTING;
    this.onerror?.();
  }

  close(): void {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }
}

const activity = (type: string, title: string) => ({
  id: `ev-${type}`,
  type,
  repository_name: 'acme/web',
  pr_number: 7,
  params: { title },
  occurred_at: '2026-11-10T12:00:00Z',
});

function connect() {
  (globalThis as { EventSource?: unknown }).EventSource = FakeEventSource;
  TestBed.configureTestingModule({ providers: [SseLiveEventGateway] });
  const gateway = TestBed.inject(SseLiveEventGateway);
  const states: SseConnectionState[] = [];
  const texts: string[] = [];
  const disconnect = gateway.connect(
    (ev) => texts.push(typeof ev.text === 'string' ? ev.text : String(ev.text.params?.['title'])),
    (state) => states.push(state),
  );
  return { states, texts, disconnect };
}

describe('SseLiveEventGateway', () => {
  const originalEventSource = (globalThis as { EventSource?: unknown }).EventSource;

  afterEach(() => {
    (globalThis as { EventSource?: unknown }).EventSource = originalEventSource;
    FakeEventSource.instances = [];
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('reports connected only once the stream opens, and delivers every activity type', () => {
    const { states, texts, disconnect } = connect();
    const es = FakeEventSource.last;
    expect(es.url).toBe('/api/v1/events/stream');
    expect(states).not.toContain('conectado');

    es.open();
    expect(states[states.length - 1]).toBe('conectado');

    // The backend sends every type as one "activity" event; types never listed before now arrive.
    es.emit('activity', activity('pr.synchronize', 'New commit on #7'));
    es.emit('activity', activity('dependabot.analyzed', 'Analyzed #7'));
    es.emit('message', activity('pr.opened', 'Opened #8'));
    expect(texts).toEqual(['New commit on #7', 'Analyzed #7', 'Opened #8']);

    disconnect();
    expect(es.closed).toBe(true);
  });

  it('reconnects with full-jitter backoff after the browser gives up, resetting once open', () => {
    vi.useFakeTimers();
    vi.spyOn(Math, 'random').mockReturnValue(1); // take the full backoff window
    const { states } = connect();
    const first = FakeEventSource.last;
    first.open();

    first.fail();
    expect(first.closed).toBe(true);
    expect(states[states.length - 1]).toBe('reconectando');
    expect(FakeEventSource.instances.length).toBe(1);

    vi.advanceTimersByTime(999);
    expect(FakeEventSource.instances.length).toBe(1);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances.length).toBe(2); // 1s after the first failure

    FakeEventSource.last.fail();
    vi.advanceTimersByTime(1999);
    expect(FakeEventSource.instances.length).toBe(2);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances.length).toBe(3); // 2s after the second

    FakeEventSource.last.open();
    expect(states[states.length - 1]).toBe('conectado');
    FakeEventSource.last.fail();
    vi.advanceTimersByTime(1000);
    expect(FakeEventSource.instances.length).toBe(4); // back to 1s after a successful open
  });

  it('lets the browser retry on its own after a network blip', () => {
    vi.useFakeTimers();
    const { states } = connect();
    FakeEventSource.last.open();
    FakeEventSource.last.blip();

    expect(states[states.length - 1]).toBe('reconectando');
    vi.advanceTimersByTime(60_000);
    expect(FakeEventSource.instances.length).toBe(1);
    expect(FakeEventSource.last.closed).toBe(false);
  });

  it('cancels a pending reconnect on disconnect', () => {
    vi.useFakeTimers();
    const { disconnect } = connect();
    FakeEventSource.last.fail();
    disconnect();
    vi.advanceTimersByTime(60_000);
    expect(FakeEventSource.instances.length).toBe(1);
  });

  it('reports disconnected and invents nothing when EventSource is unavailable', () => {
    vi.useFakeTimers();
    (globalThis as { EventSource?: unknown }).EventSource = undefined;
    TestBed.configureTestingModule({ providers: [SseLiveEventGateway] });
    const states: SseConnectionState[] = [];
    const texts: unknown[] = [];
    const disconnect = TestBed.inject(SseLiveEventGateway).connect(
      (ev) => texts.push(ev.text),
      (state) => states.push(state),
    );
    vi.advanceTimersByTime(60_000);
    expect(states).toEqual(['desconectado']);
    expect(texts).toEqual([]);
    disconnect();
  });
});
