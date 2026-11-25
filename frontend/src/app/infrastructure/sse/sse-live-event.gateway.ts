import { Injectable } from '@angular/core';
import { LiveEventGateway } from '../../domain/gateways/workspace.gateways';
import { NotificationItem } from '../../domain/models/notification.models';
import { SseConnectionState, TimelineEventItem } from '../../domain/models/workspace.models';
import { ApiNotificationDto, mapNotification } from '../http/http-notification.gateway';
import { ApiActivityEventDto, mapActivityEventToTimelineItem } from '../http/workspace-api.mapper';
import { computeFullJitterDelayMs } from '../resilience/jitter-backoff';

/** Every activity arrives as one SSE event name; its type travels in the payload. */
const SSE_EVENT_NAMES = ['activity', 'message'] as const;

/** Reconnect backoff once the browser has given up on a stream (ADR-0006). */
const RECONNECT_BACKOFF = { baseDelayMs: 1000, maxDelayMs: 30_000, maxAttempts: Number.POSITIVE_INFINITY };
const EVENT_SOURCE_CLOSED = 2;

/**
 * The server sends a `heartbeat` event every 25s. A stream that looks open but has sent nothing
 * for longer than this is dead (a proxy can keep the browser's side open after the API went away,
 * and then no error ever comes): close it and reconnect, so the reload after reconnecting catches up.
 */
const SILENCE_LIMIT_MS = 60_000;
const HEARTBEAT_EVENT_NAME = 'heartbeat';
/** The signed-in user's own notifications arrive under their own name (ADR-0017). */
const NOTIFICATION_EVENT_NAME = 'notification';

@Injectable({ providedIn: 'root' })
export class SseLiveEventGateway implements LiveEventGateway {
  connect(
    onEvent: (event: Omit<TimelineEventItem, 'key' | 't'>) => void,
    onStateChange: (state: SseConnectionState) => void,
    onNotification?: (notification: NotificationItem) => void,
  ): () => void {
    const EventSourceCtor = (globalThis as { EventSource?: typeof EventSource }).EventSource;

    if (typeof EventSourceCtor === 'function') {
      return this.connectEventSource(EventSourceCtor, onEvent, onStateChange, onNotification);
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
    onNotification?: (notification: NotificationItem) => void,
  ): () => void {
    let source: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let silenceTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;
    let stopped = false;

    const stopWatching = () => {
      if (silenceTimer !== null) {
        clearTimeout(silenceTimer);
        silenceTimer = null;
      }
    };

    const reconnectLater = () => {
      const delay = computeFullJitterDelayMs(attempt, RECONNECT_BACKOFF);
      attempt++;
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        if (!stopped) {
          open();
        }
      }, delay);
    };

    /** (Re)starts the silence countdown of an open stream: every frame proves it is alive. */
    const watch = (es: EventSource) => {
      stopWatching();
      silenceTimer = setTimeout(() => {
        silenceTimer = null;
        if (stopped || source !== es) {
          return;
        }
        es.close();
        onStateChange('reconectando');
        reconnectLater();
      }, SILENCE_LIMIT_MS);
    };

    const handleMessage = (msg: MessageEvent) => {
      if (source) {
        watch(source);
      }
      try {
        const parsed = JSON.parse(String(msg.data)) as ApiActivityEventDto;
        if (parsed && parsed.type) {
          onEvent(mapActivityEventToTimelineItem(parsed));
        }
      } catch {
        // Ignore malformed frames
      }
    };

    const handleNotification = (msg: MessageEvent) => {
      if (source) {
        watch(source);
      }
      try {
        const parsed = JSON.parse(String(msg.data)) as ApiNotificationDto;
        if (parsed && typeof parsed === 'object' && typeof parsed.id === 'number') {
          onNotification?.(mapNotification(parsed));
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
        watch(es);
        onStateChange('conectado');
      };
      es.onerror = () => {
        // Not open any more: the browser retries (and onopen watches again) or we reconnect.
        stopWatching();
        onStateChange('reconectando');
        if (es.readyState !== EVENT_SOURCE_CLOSED || stopped) {
          return; // the browser is retrying on its own
        }
        es.close();
        reconnectLater();
      };
      for (const name of SSE_EVENT_NAMES) {
        es.addEventListener(name, handleMessage as EventListener);
      }
      es.addEventListener(HEARTBEAT_EVENT_NAME, () => watch(es));
      es.addEventListener(NOTIFICATION_EVENT_NAME, handleNotification as EventListener);
    };

    onStateChange('reconectando');
    open();

    return () => {
      stopped = true;
      stopWatching();
      if (reconnectTimer !== null) {
        clearTimeout(reconnectTimer);
      }
      source?.close();
    };
  }
}
