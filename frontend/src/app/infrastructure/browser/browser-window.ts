import { InjectionToken } from '@angular/core';

/** The slice of `window` gateways need, injectable so tests never navigate. */
export interface BrowserWindow {
  readonly location: Pick<Location, 'assign' | 'search' | 'pathname' | 'hash'>;
  readonly history: Pick<History, 'replaceState'>;
}

export const BROWSER_WINDOW = new InjectionToken<BrowserWindow>('BROWSER_WINDOW', {
  providedIn: 'root',
  factory: () => window,
});
