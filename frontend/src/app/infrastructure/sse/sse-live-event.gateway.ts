import { Injectable } from '@angular/core';
import { LiveEventGateway } from '../../domain/gateways/workspace.gateways';
import { SseConnectionState, TimelineEventItem } from '../../domain/models/workspace.models';
import { ApiActivityEventDto, mapActivityEventToTimelineItem } from '../http/workspace-api.mapper';
import { computeFullJitterDelayMs } from '../resilience/jitter-backoff';

/** Every activity arrives as one SSE event name; its type travels in the payload. */
const SSE_EVENT_NAMES = ['activity', 'message'] as const;

/** Reconnect backoff once the browser has given up on a stream (ADR-0006). */
const RECONNECT_BACKOFF = { baseDelayMs: 1000, maxDelayMs: 30_000, maxAttempts: Number.POSITIVE_INFINITY };
const EVENT_SOURCE_CLOSED = 2;

@Injectable({ providedIn: 'root' })
export class SseLiveEventGateway implements LiveEventGateway {
  connect(
    onEvent: (event: Omit<TimelineEventItem, 'key' | 't'>) => void,
    onStateChange: (state: SseConnectionState) => void,
  ): () => void {
    const EventSourceCtor = (globalThis as { EventSource?: typeof EventSource }).EventSource;

    if (typeof EventSourceCtor === 'function') {
      return this.connectEventSource(EventSourceCtor, onEvent, onStateChange);
    }

    // No EventSource (very old browser or a non-browser runtime): say so, never invent events.
    onStateChange('desconectado');
    return () => undefined;
  }

  /**
   * The browser retries a dropped connection by itself, but gives up for good on a non-200
   * response (a 502 while the API restarts, a 401 after the session expires). Then we
   * reconnect ourselves with full-jitter backoff, reset after every successful open.
   */
  private connectEventSource(
    EventSourceCtor: typeof EventSource,
    onEvent: (event: Omit<TimelineEventItem, 'key' | 't'>) => void,
    onStateChange: (state: SseConnectionState) => void,
  ): () => void {
    let source: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;
    let stopped = false;

    const handleMessage = (msg: MessageEvent) => {
      try {
        const parsed = JSON.parse(String(msg.data)) as ApiActivityEventDto;
        if (parsed && parsed.summary) {
          onEvent(mapActivityEventToTimelineItem(parsed));
        }
      } catch {
        // Ignore malformed frames
      }
    };

    const open = () => {
      const es = new EventSourceCtor('/api/v1/events/stream');
      source = es;
      es.onopen = () => {
        attempt = 0;
        onStateChange('conectado');
      };
      es.onerror = () => {
        onStateChange('reconectando');
        if (es.readyState !== EVENT_SOURCE_CLOSED || stopped) {
          return; // the browser is retrying on its own
        }
        es.close();
        const delay = computeFullJitterDelayMs(attempt, RECONNECT_BACKOFF);
        attempt++;
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null;
          if (!stopped) {
            open();
          }
        }, delay);
      };
      for (const name of SSE_EVENT_NAMES) {
        es.addEventListener(name, handleMessage as EventListener);
      }
    };

    onStateChange('reconectando');
    open();

    return () => {
      stopped = true;
      if (reconnectTimer !== null) {
        clearTimeout(reconnectTimer);
      }
      source?.close();
    };
  }
}
