import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NOTIFICATION_DEVICE,
  NOTIFICATION_GATEWAY,
  NotificationDevice,
  NotificationGateway,
} from '../../domain/gateways/notification.gateways';
import { BrowserPermission, NotificationItem, NotificationOptions, SystemPopup } from '../../domain/models/notification.models';
import { LocaleStore } from './locale.store';
import { NotificationStore, POPUP_BURST_MS } from './notification.store';

const T0 = Date.parse('2026-11-23T09:00:00Z');

function item(id: number, patch: Partial<NotificationItem> = {}): NotificationItem {
  return {
    id,
    kind: 'turn',
    reason: 'review_requested',
    repositoryId: 10,
    repositoryName: 'arvore/hive-web',
    pullRequestId: 100 + id,
    prNumber: id,
    prTitle: `PR ${id}`,
    actorLogin: 'carol',
    details: {},
    createdAt: T0 + id * 1000,
    read: false,
    popUp: true,
    ...patch,
  };
}

const OPTIONS: NotificationOptions = {
  settings: { paused: false, popupsOff: [] },
  channels: ['panel'],
  kinds: ['turn', 'risk', 'involved'],
};

class FakeGateway implements NotificationGateway {
  page = { items: [item(3), item(2), item(1, { read: true })] as NotificationItem[], unread: 12, nextBefore: 1 as number | null };
  saved: unknown[] = [];
  read: unknown[] = [];
  list = vi.fn(async (before: number | null) =>
    before === null ? this.page : { items: [item(0, { read: true })], unread: this.page.unread, nextBefore: null },
  );
  markRead = vi.fn(async (ids: readonly number[] | 'all') => {
    this.read.push(ids);
  });
  options = vi.fn(async () => OPTIONS);
  saveSettings = vi.fn(async (s: NotificationOptions['settings']) => {
    this.saved.push(s);
    return { ...OPTIONS, settings: s };
  });
}

class FakeDevice implements NotificationDevice {
  perm: BrowserPermission = 'granted';
  visible = false;
  shown: { popup: SystemPopup; onClick: () => void }[] = [];
  shared: unknown[] = [];
  visibleFns: (() => void)[] = [];
  sharedFns: ((ids: readonly number[] | 'all') => void)[] = [];
  permission = () => this.perm;
  requestPermission = vi.fn(async () => {
    this.perm = 'granted';
    return this.perm;
  });
  show = (popup: SystemPopup, onClick: () => void) => {
    this.shown.push({ popup, onClick });
  };
  isVisible = () => this.visible;
  onVisible = (fn: () => void) => {
    this.visibleFns.push(fn);
    return () => undefined;
  };
  shareRead = (ids: readonly number[] | 'all') => {
    this.shared.push(ids);
  };
  onSharedRead = (fn: (ids: readonly number[] | 'all') => void) => {
    this.sharedFns.push(fn);
    return () => undefined;
  };
}

describe('NotificationStore', () => {
  let gateway: FakeGateway;
  let device: FakeDevice;
  let store: NotificationStore;
  let t: LocaleStore;

  beforeEach(async () => {
    gateway = new FakeGateway();
    device = new FakeDevice();
    TestBed.configureTestingModule({
      providers: [
        { provide: NOTIFICATION_GATEWAY, useValue: gateway },
        { provide: NOTIFICATION_DEVICE, useValue: device },
      ],
    });
    store = TestBed.inject(NotificationStore);
    t = TestBed.inject(LocaleStore);
    await store.start();
  });

  afterEach(() => {
    store.stop();
    vi.useRealTimers();
    TestBed.resetTestingModule();
  });

  it('loads the newest page, the settings and the browser permission', () => {
    expect(store.items().map((n) => n.id)).toEqual([3, 2, 1]);
    expect(store.unread()).toBe(12);
    expect(store.badge()).toBe('9+');
    expect(store.status()).toBe('active');
    expect(store.canLoadMore()).toBe(true);
  });

  it('loads older pages after the newest', async () => {
    await store.loadMore();
    expect(store.items().map((n) => n.id)).toEqual([3, 2, 1, 0]);
    expect(store.canLoadMore()).toBe(false);
  });

  it('tells each permission state apart, pause included', async () => {
    const statusFor = (perm: BrowserPermission) => {
      device.perm = perm;
      store.refreshPermission();
      return store.status();
    };
    expect(statusFor('unsupported')).toBe('unsupported');
    expect(statusFor('default')).toBe('default');
    expect(statusFor('denied')).toBe('denied');
    expect(statusFor('granted')).toBe('active');
    await store.setPaused(true);
    expect(store.status()).toBe('paused');
    expect(gateway.saved).toEqual([{ paused: true, popupsOff: [] }]);
  });

  it('asks the browser for permission only when asked to', async () => {
    device.perm = 'default';
    store.refreshPermission();
    await store.enable();
    expect(device.requestPermission).toHaveBeenCalledTimes(1);
    expect(store.status()).toBe('active');
  });

  it('switches the popups of one kind', async () => {
    await store.togglePopups('risk');
    expect(store.options()?.settings.popupsOff).toEqual(['risk']);
    await store.togglePopups('risk');
    expect(gateway.saved).toEqual([
      { paused: false, popupsOff: ['risk'] },
      { paused: false, popupsOff: [] },
    ]);
  });

  it('adds a live notification once, counts it unread, and groups a burst into one popup per kind', () => {
    vi.useFakeTimers();
    store.receive(item(4));
    store.receive(item(4)); // the same one again (another replica, a reconnect)
    store.receive(item(5));
    store.receive(item(6, { kind: 'risk', reason: 'high_risk', details: { package: 'lodash', from: '4', to: '5' } }));
    expect(store.items().map((n) => n.id)).toEqual([6, 5, 4, 3, 2, 1]);
    expect(store.unread()).toBe(15);
    expect(device.shown).toHaveLength(0);

    vi.advanceTimersByTime(POPUP_BURST_MS);
    expect(device.shown.map((s) => s.popup.tag)).toEqual(['pf-turn', 'pf-risk']);
    expect(device.shown[0].popup.title).toBe(t.translate('notifications.group.turn', { count: 2 }));
    expect(device.shown[0].popup.body).toContain('PR 5');
    expect(device.shown[1].popup.title).toBe(t.translate('notifications.popup.risk', { repo: 'arvore/hive-web', pr: 6 }));
    expect(device.shown[1].popup.body).toBe(t.translate('notifications.reason.high_risk', { package: 'lodash', from: '4', to: '5' }) + '\nPR 6');
  });

  it('pops up nothing while the tab is visible, when not allowed, or when the owner turned it off', () => {
    vi.useFakeTimers();
    device.visible = true;
    store.receive(item(4));
    device.visible = false;
    store.receive(item(5, { popUp: false }));
    device.perm = 'denied';
    store.refreshPermission();
    store.receive(item(6));
    vi.advanceTimersByTime(POPUP_BURST_MS);
    expect(device.shown).toHaveLength(0);
    expect(store.unread()).toBe(15);
  });

  it('opens what a popup is about and marks it read', async () => {
    vi.useFakeTimers();
    const opened: number[][] = [];
    store.setOpener((items) => opened.push(items.map((n) => n.id)));
    store.receive(item(4));
    vi.advanceTimersByTime(POPUP_BURST_MS);
    device.shown[0].onClick();
    expect(opened).toEqual([[4]]);
    expect(gateway.read).toEqual([[4]]);
    expect(store.items()[0].read).toBe(true);
    expect(store.unread()).toBe(12); // 12, one more arrived, that one read
  });

  it('marks everything read here and in the other tabs', async () => {
    await store.markAllRead();
    expect(store.unread()).toBe(0);
    expect(store.items().every((n) => n.read)).toBe(true);
    expect(gateway.read).toEqual(['all']);
    expect(device.shared).toEqual(['all']);
  });

  it('follows what another tab read without asking the server again', () => {
    device.sharedFns[0]([3]);
    expect(store.items().find((n) => n.id === 3)?.read).toBe(true);
    expect(store.unread()).toBe(11);
    expect(gateway.read).toEqual([]);
  });

  it('reloads when the tab is shown again', async () => {
    gateway.page = { items: [item(9)], unread: 1, nextBefore: null };
    device.visibleFns[0]();
    await vi.waitFor(() => expect(store.items().map((n) => n.id)).toEqual([9]));
  });

  it('groups consecutive notifications of one kind into one entry of the center', () => {
    store.receive(item(4, { popUp: false }));
    const groups = store.groups();
    expect(groups[0].items.map((n) => n.id)).toEqual([4, 3, 2, 1]);
    expect(groups[0].title).toBe(t.translate('notifications.group.turn', { count: 4 }));
    store.receive(item(5, { kind: 'involved', reason: 'merged', popUp: false }));
    expect(store.groups().map((g) => g.items.length)).toEqual([1, 4]);
    expect(store.groups()[0].items[0].line).toBe(t.translate('notifications.reason.merged', { actor: 'carol' }));
  });

  it('sizes the panel within its bounds, steps it, resets it and remembers it', () => {
    store.resetWidth();
    expect(store.width()).toBe(400);
    store.startResize();
    expect(store.isResizing()).toBe(true);
    store.setWidth(520, 900);
    expect(store.width()).toBe(520);
    store.setWidth(100, 900);
    expect(store.width()).toBe(320);
    store.setWidth(2000, 700);
    expect(store.width()).toBe(700);
    store.endResize();
    expect(store.isResizing()).toBe(false);
    store.resetWidth();
    store.stepWidth(24, 900);
    expect(store.width()).toBe(424);
    expect(localStorage.getItem('pf-notif-w')).toBe('424');
    store.resetWidth();
  });

  it('tells the workspace when the center opens, not when it closes', () => {
    let opened = 0;
    store.setOnOpen(() => opened++);
    store.toggleOpen();
    store.toggleOpen();
    expect(opened).toBe(1);
  });

  it('forgets everything when the session ends', () => {
    store.stop();
    expect(store.items()).toEqual([]);
    expect(store.unread()).toBe(0);
    expect(store.isOpen()).toBe(false);
  });
});
