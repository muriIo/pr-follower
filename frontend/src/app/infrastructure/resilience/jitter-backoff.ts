export interface BackoffConfig {
  readonly baseDelayMs: number;
  readonly maxDelayMs: number;
  readonly maxAttempts: number;
}

export class PermanentError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PermanentError';
  }
}

export function computeFullJitterDelayMs(
  attempt: number,
  config: BackoffConfig,
  randomFn: () => number = Math.random,
): number {
  const expDelay = config.baseDelayMs * Math.pow(2, attempt);
  const capped = Math.min(config.maxDelayMs, expDelay);
  return Math.floor(capped * randomFn());
}

export async function retryWithJitter<T>(
  fn: () => Promise<T>,
  config: BackoffConfig = { baseDelayMs: 80, maxDelayMs: 2500, maxAttempts: 4 },
  signal?: AbortSignal,
): Promise<T> {
  let lastError: unknown;
  for (let attempt = 0; attempt < config.maxAttempts; attempt++) {
    if (signal?.aborted) {
      throw new DOMException('Aborted', 'AbortError');
    }
    try {
      return await fn();
    } catch (err: unknown) {
      lastError = err;
      if (err instanceof PermanentError || (err instanceof DOMException && err.name === 'AbortError')) {
        throw err;
      }
      if (attempt === config.maxAttempts - 1) {
        break;
      }
      const sleepMs = computeFullJitterDelayMs(attempt, config);
      await waitDelayWithAbort(sleepMs, signal);
    }
  }
  throw lastError;
}

function waitDelayWithAbort(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException('Aborted', 'AbortError'));
      return;
    }
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener(
      'abort',
      () => {
        clearTimeout(timer);
        reject(new DOMException('Aborted', 'AbortError'));
      },
      { once: true },
    );
  });
}
