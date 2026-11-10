import { Injectable } from '@angular/core';
import { LiveEventGateway } from '../../domain/gateways/workspace.gateways';
import { SseConnectionState, TimelineEventItem } from '../../domain/models/workspace.models';
import { ApiActivityEventDto, mapActivityEventToTimelineItem } from '../http/workspace-api.mapper';

const SSE_EVENT_TYPES = [
  'message',
  'workspace.seeded',
  'repository.followed',
  'repository.access_lost',
  'repository.access_suspended',
  'repository.access_restored',
  'pr.approved',
  'pr.merged',
  'pr.snoozed',
  'pr.claimed',
  'dependabot.command',
  'pr.updated',
] as const;

const FALLBACK_EVENT_POOL: readonly Omit<TimelineEventItem, 'key' | 't'>[] = [
  { kind: 'info', text: '@bianca-dev enviou um commit para #142', repo: 'arvore/hive-web', pr: 142 },
  { kind: 'ok', text: 'CI passou em #57', repo: 'arvore/design-system', pr: 57 },
  { kind: 'info', text: 'Tiago Melo respondeu um comentário em #301', repo: 'arvore/auth-service', pr: 301 },
  { kind: 'info', text: 'Dependabot atualizou o lockfile de #88', repo: 'arvore/design-system', pr: 88 },
  { kind: 'ok', text: '@rafael aprovou #408', repo: 'arvore/checkout-api', pr: 408 },
];

@Injectable({ providedIn: 'root' })
export class SseLiveEventGateway implements LiveEventGateway {
  private intervalMs = 7000;
  private liveIndex = 0;

  setIntervalMs(ms: number): void {
    this.intervalMs = ms;
  }

  connect(
    onEvent: (event: Omit<TimelineEventItem, 'key' | 't'>) => void,
    onStateChange: (state: SseConnectionState) => void,
  ): () => void {
    const EventSourceCtor = (globalThis as { EventSource?: typeof EventSource }).EventSource;

    if (typeof EventSourceCtor === 'function') {
      const es = new EventSourceCtor('/api/v1/events/stream');
      onStateChange('conectado');

      es.onopen = () => {
        onStateChange('conectado');
      };

      es.onerror = () => {
        onStateChange('reconectando');
      };

      const handleMessage = (msg: MessageEvent) => {
        try {
          const parsed = JSON.parse(String(msg.data)) as ApiActivityEventDto;
          if (parsed && parsed.summary) {
            onEvent(mapActivityEventToTimelineItem(parsed));
          }
        } catch {
          // Ignore malformed SSE comment/heartbeat frames
        }
      };

      for (const eventType of SSE_EVENT_TYPES) {
        es.addEventListener(eventType, handleMessage as EventListener);
      }

      return () => {
        es.close();
      };
    }

    onStateChange('conectado');
    const timer = setInterval(() => {
      const next = FALLBACK_EVENT_POOL[this.liveIndex++ % FALLBACK_EVENT_POOL.length];
      onEvent(next);
    }, this.intervalMs);

    return () => {
      clearInterval(timer);
    };
  }
}
