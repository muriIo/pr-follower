import { InjectionToken } from '@angular/core';
import { ActionRequestOptions } from './workspace.gateways';
import {
  BrowserPermission,
  NotificationOptions,
  NotificationPage,
  NotificationSettings,
  SystemPopup,
} from '../models/notification.models';

/** The API's notification center (ADR-0017). Every call rejects with an ActionError. */
export interface NotificationGateway {
  list(before: number | null, options?: ActionRequestOptions): Promise<NotificationPage>;
  /** Marks the given notifications read, or all of them when `ids` is 'all'. */
  markRead(ids: readonly number[] | 'all'): Promise<void>;
  options(options?: ActionRequestOptions): Promise<NotificationOptions>;
  saveSettings(settings: NotificationSettings): Promise<NotificationOptions>;
}

/** This browser and tab: the system notification permission, popups and the other open tabs. */
export interface NotificationDevice {
  permission(): BrowserPermission;
  /** Asks the browser; call it from a click. Resolves with the permission it ends with. */
  requestPermission(): Promise<BrowserPermission>;
  /** Shows a system popup; onClick runs when the user clicks it (after the tab is focused). */
  show(popup: SystemPopup, onClick: () => void): void;
  /** Whether the user is looking at this tab now. */
  isVisible(): boolean;
  /** Runs fn each time this tab becomes visible again; returns the unsubscribe. */
  onVisible(fn: () => void): () => void;
  /** Tells the other open tabs of this browser what was read here. */
  shareRead(ids: readonly number[] | 'all'): void;
  /** Runs fn when another tab read notifications; returns the unsubscribe. */
  onSharedRead(fn: (ids: readonly number[] | 'all') => void): () => void;
}

export const NOTIFICATION_GATEWAY = new InjectionToken<NotificationGateway>('NOTIFICATION_GATEWAY');
export const NOTIFICATION_DEVICE = new InjectionToken<NotificationDevice>('NOTIFICATION_DEVICE');
