import { describe, expect, it } from 'vitest';
import {
  PermanentError,
  computeFullJitterDelayMs,
  retryWithJitter,
} from './jitter-backoff';

describe('Full-Jitter Exponential Backoff (jitter-backoff)', () => {
  it('computes full-jitter delay bounded by maxDelayMs', () => {
    const cfg = { baseDelayMs: 100, maxDelayMs: 500, maxAttempts: 4 };
    // attempt 0 -> cap 100 * 0.5 = 50
    expect(computeFullJitterDelayMs(0, cfg, () => 0.5)).toBe(50);
    // attempt 3 -> cap min(500, 800) * 1.0 = 500
    expect(computeFullJitterDelayMs(3, cfg, () => 1.0)).toBe(500);
  });

  it('retries transient errors and succeeds on subsequent attempt', async () => {
    let attempts = 0;
    const result = await retryWithJitter(
      async () => {
        attempts++;
        if (attempts < 3) {
          throw new Error('503 Service Unavailable');
        }
        return 'ok';
      },
      { baseDelayMs: 2, maxDelayMs: 10, maxAttempts: 4 },
    );

    expect(result).toBe('ok');
    expect(attempts).toBe(3);
  });

  it('stops immediately on PermanentError or AbortSignal cancellation', async () => {
    let attempts = 0;
    await expect(
      retryWithJitter(
        async () => {
          attempts++;
          throw new PermanentError('404 Not Found');
        },
        { baseDelayMs: 2, maxDelayMs: 10, maxAttempts: 4 },
      ),
    ).rejects.toThrow('404 Not Found');
    expect(attempts).toBe(1);

    const ctrl = new AbortController();
    ctrl.abort();
    await expect(
      retryWithJitter(async () => 'never', { baseDelayMs: 2, maxDelayMs: 10, maxAttempts: 3 }, ctrl.signal),
    ).rejects.toThrow();
  });
});
