import { afterEach, describe, expect, it, vi } from 'vitest';
import { BrowserNotificationDevice } from './browser-notification-device';

class FakeNotification {
  static permission: NotificationPermission = 'default';
  static requestPermission = vi.fn(async () => 'granted' as NotificationPermission);
  static shown: FakeNotification[] = [];
  onclick: (() => void) | null = null;
  constructor(
    readonly title: string,
    readonly options: NotificationOptions & { renotify?: boolean },
  ) {
    FakeNotification.shown.push(this);
  }
  close = vi.fn();
}

class FakeChannel {
  static all: FakeChannel[] = [];
  onmessage: ((e: MessageEvent) => void) | null = null;
  constructor(readonly name: string) {
    FakeChannel.all.push(this);
  }
  postMessage(data: unknown): void {
    for (const other of FakeChannel.all) {
      if (other !== this && other.name === this.name) {
        other.onmessage?.({ data } as MessageEvent);
      }
    }
  }
  close(): void {
    FakeChannel.all = FakeChannel.all.filter((c) => c !== this);
  }
}

describe('BrowserNotificationDevice', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    FakeNotification.shown = [];
    FakeNotification.permission = 'default';
    FakeChannel.all = [];
  });

  it('is unsupported without the Notification API or outside a secure context', () => {
    vi.stubGlobal('Notification', undefined);
    expect(new BrowserNotificationDevice().permission()).toBe('unsupported');

    vi.stubGlobal('Notification', FakeNotification);
    vi.stubGlobal('isSecureContext', false);
    expect(new BrowserNotificationDevice().permission()).toBe('unsupported');
  });

  it('reports and requests the browser permission', async () => {
    vi.stubGlobal('Notification', FakeNotification);
    vi.stubGlobal('isSecureContext', true);
    const device = new BrowserNotificationDevice();
    expect(device.permission()).toBe('default');
    FakeNotification.requestPermission.mockImplementationOnce(async () => {
      FakeNotification.permission = 'denied';
      return 'denied';
    });
    expect(await device.requestPermission()).toBe('denied');
    expect(device.permission()).toBe('denied');
  });

  it('shows a popup that replaces the previous one of its tag, and focuses the tab on click', () => {
    vi.stubGlobal('Notification', FakeNotification);
    vi.stubGlobal('isSecureContext', true);
    FakeNotification.permission = 'granted';
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => undefined);
    const onClick = vi.fn();

    new BrowserNotificationDevice().show({ title: 'Sua vez', body: '#35 Fix login', tag: 'pf-turn' }, onClick);

    const [popup] = FakeNotification.shown;
    expect(popup.title).toBe('Sua vez');
    expect(popup.options).toMatchObject({ body: '#35 Fix login', tag: 'pf-turn', renotify: true });
    popup.onclick?.();
    expect(focus).toHaveBeenCalled();
    expect(onClick).toHaveBeenCalled();
    expect(popup.close).toHaveBeenCalled();
  });

  it('shows nothing without the permission', () => {
    vi.stubGlobal('Notification', FakeNotification);
    vi.stubGlobal('isSecureContext', true);
    new BrowserNotificationDevice().show({ title: 't', body: 'b', tag: 'x' }, () => undefined);
    expect(FakeNotification.shown).toHaveLength(0);
  });

  it('shares what was read with the other tabs', () => {
    vi.stubGlobal('BroadcastChannel', FakeChannel);
    const here = new BrowserNotificationDevice();
    const there = new BrowserNotificationDevice();
    const seen: unknown[] = [];
    const stop = there.onSharedRead((ids) => seen.push(ids));

    here.shareRead([1, 2]);
    here.shareRead('all');
    stop();
    here.shareRead([3]);
    expect(seen).toEqual([[1, 2], 'all']);
  });

  it('tells when the tab is visible again', () => {
    const device = new BrowserNotificationDevice();
    const fn = vi.fn();
    const stop = device.onVisible(fn);
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    expect(device.isVisible()).toBe(true);
    stop();
    document.dispatchEvent(new Event('visibilitychange'));
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
