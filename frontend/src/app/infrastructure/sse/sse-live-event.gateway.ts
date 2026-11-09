import { Injectable } from '@angular/core';
import { LiveEventGateway } from '../../domain/gateways/workspace.gateways';
import { SseConnectionState, TimelineEventItem } from '../../domain/models/workspace.models';

const LIVE_EVENT_POOL: readonly Omit<TimelineEventItem, 'key' | 't'>[] = [
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
    onStateChange('conectado');
    const timer = setInterval(() => {
      const next = LIVE_EVENT_POOL[this.liveIndex++ % LIVE_EVENT_POOL.length];
      onEvent(next);
    }, this.intervalMs);

    return () => {
      clearInterval(timer);
    };
  }
}
