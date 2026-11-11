import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { SessionExpiryMonitor } from './session-expiry.monitor';

describe('SessionExpiryMonitor', () => {
  it('tells the listener when an API call was refused as unauthenticated', () => {
    const monitor = TestBed.inject(SessionExpiryMonitor);
    const heard = vi.fn();
    monitor.listen(heard);

    monitor.report();
    monitor.report('github_token_expired');

    expect(heard).toHaveBeenNthCalledWith(1, 'unauthorized');
    expect(heard).toHaveBeenNthCalledWith(2, 'github_token_expired');
  });

  it('does nothing when nobody listens yet', () => {
    expect(() => TestBed.inject(SessionExpiryMonitor).report()).not.toThrow();
  });
});
