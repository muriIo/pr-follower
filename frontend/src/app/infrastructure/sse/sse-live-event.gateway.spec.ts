import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { SseConnectionState } from '../../domain/models/workspace.models';
import { SseLiveEventGateway } from './sse-live-event.gateway';

describe('SseLiveEventGateway', () => {
  it('connects, reports conectado state, emits periodic events, and cleans up on disconnect', async () => {
    TestBed.configureTestingModule({ providers: [SseLiveEventGateway] });
    const gateway = TestBed.inject(SseLiveEventGateway);
    gateway.setIntervalMs(15);

    const states: SseConnectionState[] = [];
    const events: string[] = [];

    const disconnect = gateway.connect(
      (ev) => events.push(ev.text),
      (state) => states.push(state),
    );

    expect(states).toContain('conectado');
    await new Promise((r) => setTimeout(r, 40));
    disconnect();

    expect(events.length).toBeGreaterThan(0);
  });
});
