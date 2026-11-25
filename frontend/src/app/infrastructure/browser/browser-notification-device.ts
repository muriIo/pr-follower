import { Injectable } from '@angular/core';
import { NotificationDevice } from '../../domain/gateways/notification.gateways';
import { BrowserPermission, SystemPopup } from '../../domain/models/notification.models';

/** The tabs of this browser tell each other what was read here, so their unread counts agree. */
const READ_CHANNEL = 'pr-follower-notifications';

type NotificationCtor = {
  readonly permission: NotificationPermission;
  requestPermission(): Promise<NotificationPermission>;
  new (title: string, options?: NotificationOptions & { renotify?: boolean }): Notification;
};

/** The Notification API, page visibility and BroadcastChannel of this browser (MDN). */
@Injectable({ providedIn: 'root' })
export class BrowserNotificationDevice implements NotificationDevice {
  private channel: BroadcastChannel | null = null;

  permission(): BrowserPermission {
    const api = this.api();
    return api ? (api.permission as BrowserPermission) : 'unsupported';
  }

  async requestPermission(): Promise<BrowserPermission> {
    const api = this.api();
    if (!api) {
      return 'unsupported';
    }
    try {
      await api.requestPermission();
    } catch {
      // An old browser that only takes a callback: its answer is in permission either way.
    }
    return this.permission();
  }

  show(popup: SystemPopup, onClick: () => void): void {
    const api = this.api();
    if (!api || api.permission !== 'granted') {
      return;
    }
    try {
      const shown = new api(popup.title, { body: popup.body, tag: popup.tag, renotify: true });
      shown.onclick = () => {
        window.focus();
        onClick();
        shown.close();
      };
    } catch {
      // A browser that shows notifications only through a service worker (mobile): the center still lists it.
    }
  }

  isVisible(): boolean {
    return typeof document !== 'undefined' && document.visibilityState === 'visible';
  }

  onVisible(fn: () => void): () => void {
    const listener = () => {
      if (this.isVisible()) {
        fn();
      }
    };
    document.addEventListener('visibilitychange', listener);
    return () => document.removeEventListener('visibilitychange', listener);
  }

  shareRead(ids: readonly number[] | 'all'): void {
    this.readChannel()?.postMessage(ids === 'all' ? 'all' : [...ids]);
  }

  onSharedRead(fn: (ids: readonly number[] | 'all') => void): () => void {
    const Ctor = (globalThis as { BroadcastChannel?: typeof BroadcastChannel }).BroadcastChannel;
    if (typeof Ctor !== 'function') {
      return () => undefined;
    }
    const listener = new Ctor(READ_CHANNEL);
    listener.onmessage = (e: MessageEvent) => {
      const data: unknown = e.data;
      if (data === 'all') {
        fn('all');
      } else if (Array.isArray(data)) {
        fn(data.filter((id): id is number => typeof id === 'number'));
      }
    };
    return () => listener.close();
  }

  private readChannel(): BroadcastChannel | null {
    const Ctor = (globalThis as { BroadcastChannel?: typeof BroadcastChannel }).BroadcastChannel;
    if (typeof Ctor !== 'function') {
      return null;
    }
    this.channel ??= new Ctor(READ_CHANNEL);
    return this.channel;
  }

  private api(): NotificationCtor | null {
    const api = (globalThis as { Notification?: NotificationCtor }).Notification;
    const secure = (globalThis as { isSecureContext?: boolean }).isSecureContext !== false;
    return typeof api === 'function' && secure ? api : null;
  }
}
