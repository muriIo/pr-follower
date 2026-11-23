import { Injectable, computed, inject, signal } from '@angular/core';
import { NOTIFICATION_DEVICE, NOTIFICATION_GATEWAY } from '../../domain/gateways/notification.gateways';
import { ActionError } from '../../domain/models/action-error.models';
import {
  BrowserPermission,
  NotificationItem,
  NotificationKind,
  NotificationOptions,
  NotificationSettings,
  SystemPopup,
} from '../../domain/models/notification.models';
import { LocaleStore } from './locale.store';

/** Live notifications arriving this close together pop up as one per kind. */
export const POPUP_BURST_MS = 1500;
/** Neighbouring notifications of one kind this close in time are one entry of the center. */
const CENTER_BURST_MS = 2 * 60_000;
/** How many PR titles a grouped popup lists. */
const POPUP_TITLES = 3;

/** One of the five states the settings show (the browser permission, then the user's pause). */
export type NotificationStatus = 'unsupported' | 'default' | 'active' | 'paused' | 'denied';

export interface NotificationRowViewModel {
  readonly id: number;
  readonly kind: NotificationKind;
  readonly reason: NotificationItem['reason'];
  readonly read: boolean;
  readonly heading: string;
  readonly line: string;
  readonly title: string;
  readonly createdAt: number;
}

export interface NotificationGroupViewModel {
  readonly key: string;
  readonly kind: NotificationKind;
  readonly title: string;
  readonly unread: number;
  readonly items: readonly NotificationRowViewModel[];
}

/**
 * The notification center (ADR-0017): what the API created for the signed-in user, its read
 * state, the settings, and the system popups of this tab.
 */
@Injectable({ providedIn: 'root' })
export class NotificationStore {
  private readonly gateway = inject(NOTIFICATION_GATEWAY, { optional: true });
  private readonly device = inject(NOTIFICATION_DEVICE, { optional: true });
  private readonly locale = inject(LocaleStore);

  readonly items = signal<readonly NotificationItem[]>([]);
  readonly unread = signal(0);
  readonly options = signal<NotificationOptions | null>(null);
  readonly permission = signal<BrowserPermission>('unsupported');
  readonly isOpen = signal(false);
  readonly tab = signal<'list' | 'settings'>('list');
  readonly loadError = signal<ActionError | null>(null);
  readonly expanded = signal<ReadonlySet<string>>(new Set());
  private readonly nextBefore = signal<number | null>(null);

  readonly canLoadMore = computed(() => this.nextBefore() !== null);
  readonly badge = computed(() => {
    const n = this.unread();
    return n <= 0 ? '' : n > 9 ? '9+' : String(n);
  });
  readonly status = computed<NotificationStatus>(() => {
    const perm = this.permission();
    if (perm !== 'granted') {
      return perm;
    }
    return this.options()?.settings.paused ? 'paused' : 'active';
  });
  readonly pushOffered = computed(() => this.options()?.channels.includes('webpush') ?? false);
  readonly kinds = computed(() =>
    (this.options()?.kinds ?? []).map((kind) => ({
      kind,
      label: this.locale.translate(`notifications.kind.${kind}`),
      hint: this.locale.translate(`notifications.kind.${kind}.hint`),
      on: !(this.options()?.settings.popupsOff ?? []).includes(kind),
    })),
  );

  /** Neighbouring notifications of one kind are one entry, newest first. */
  readonly groups = computed<readonly NotificationGroupViewModel[]>(() => {
    this.locale.dictionary(); // re-render on a language switch
    const groups: { kind: NotificationKind; items: NotificationItem[] }[] = [];
    for (const n of this.items()) {
      const last = groups[groups.length - 1];
      const prev = last?.items[last.items.length - 1];
      if (last && last.kind === n.kind && prev && prev.createdAt - n.createdAt <= CENTER_BURST_MS) {
        last.items.push(n);
      } else {
        groups.push({ kind: n.kind, items: [n] });
      }
    }
    return groups.map((g) => ({
      key: `${g.kind}-${g.items[0].id}`,
      kind: g.kind,
      title:
        g.items.length === 1
          ? this.heading(g.items[0])
          : this.locale.translate(`notifications.group.${g.kind}`, { count: g.items.length }),
      unread: g.items.filter((n) => !n.read).length,
      items: g.items.map((n) => this.row(n)),
    }));
  });

  private opener: ((items: readonly NotificationItem[]) => void) | null = null;
  private popupBuffer: NotificationItem[] = [];
  private popupTimer: ReturnType<typeof setTimeout> | null = null;
  private unsubscribers: (() => void)[] = [];
  private started = false;

  /** Loads the center and starts following this tab and the others. */
  async start(): Promise<void> {
    if (this.started) {
      return;
    }
    this.started = true;
    this.refreshPermission();
    if (this.device) {
      this.unsubscribers.push(
        this.device.onVisible(() => {
          this.refreshPermission();
          void this.refresh();
        }),
        this.device.onSharedRead((ids) => this.applyRead(ids)),
      );
    }
    await Promise.all([this.refresh(), this.loadOptions()]);
  }

  /** Forgets everything (sign-out). */
  stop(): void {
    this.started = false;
    this.unsubscribers.forEach((stop) => stop());
    this.unsubscribers = [];
    if (this.popupTimer !== null) {
      clearTimeout(this.popupTimer);
      this.popupTimer = null;
    }
    this.popupBuffer = [];
    this.items.set([]);
    this.unread.set(0);
    this.nextBefore.set(null);
    this.options.set(null);
    this.isOpen.set(false);
    this.loadError.set(null);
  }

  /** What opening notifications does (the workspace decides: the drawer, a perspective). */
  setOpener(fn: (items: readonly NotificationItem[]) => void): void {
    this.opener = fn;
  }

  /** Reloads the newest page: after a reconnect or when the tab is shown again. */
  async refresh(): Promise<void> {
    if (!this.gateway) {
      return;
    }
    try {
      const page = await this.gateway.list(null);
      this.items.set(page.items);
      this.unread.set(page.unread);
      this.nextBefore.set(page.nextBefore);
      this.loadError.set(null);
    } catch (err: unknown) {
      this.loadError.set(err instanceof ActionError ? err : new ActionError('unknown'));
    }
  }

  async loadMore(): Promise<void> {
    const before = this.nextBefore();
    if (!this.gateway || before === null) {
      return;
    }
    try {
      const page = await this.gateway.list(before);
      const known = new Set(this.items().map((n) => n.id));
      this.items.update((items) => [...items, ...page.items.filter((n) => !known.has(n.id))]);
      this.nextBefore.set(page.nextBefore);
    } catch (err: unknown) {
      this.loadError.set(err instanceof ActionError ? err : new ActionError('unknown'));
    }
  }

  /** A notification delivered live: listed at once, and popped up if its owner and the browser allow. */
  receive(n: NotificationItem): void {
    if (this.items().some((known) => known.id === n.id)) {
      return;
    }
    this.items.update((items) => [n, ...items]);
    if (!n.read) {
      this.unread.update((c) => c + 1);
    }
    if (n.popUp && this.permission() === 'granted' && this.device && !this.device.isVisible()) {
      this.popupBuffer.push(n);
      this.popupTimer ??= setTimeout(() => this.flushPopups(), POPUP_BURST_MS);
    }
  }

  async markRead(ids: readonly number[]): Promise<void> {
    const unread = ids.filter((id) => this.items().some((n) => n.id === id && !n.read));
    if (unread.length === 0) {
      return;
    }
    this.applyRead(unread);
    this.device?.shareRead(unread);
    await this.gateway?.markRead(unread).catch(() => undefined); // read state is a convenience: a failure only leaves it unread
  }

  async markAllRead(): Promise<void> {
    this.applyRead('all');
    this.device?.shareRead('all');
    await this.gateway?.markRead('all').catch(() => undefined);
  }

  /** Opens what the notifications are about, marking them read. */
  activate(ids: readonly number[]): void {
    const items = this.items().filter((n) => ids.includes(n.id));
    if (items.length === 0) {
      return;
    }
    void this.markRead(ids);
    this.isOpen.set(false);
    this.opener?.(items);
  }

  refreshPermission(): void {
    this.permission.set(this.device?.permission() ?? 'unsupported');
  }

  /** Asks the browser for permission: only from the user's click. */
  async enable(): Promise<void> {
    if (!this.device) {
      return;
    }
    this.permission.set(await this.device.requestPermission());
    if (this.options()?.settings.paused) {
      await this.setPaused(false);
    }
  }

  async setPaused(paused: boolean): Promise<void> {
    await this.save({ ...this.currentSettings(), paused });
  }

  async togglePopups(kind: NotificationKind): Promise<void> {
    const current = this.currentSettings();
    const popupsOff = current.popupsOff.includes(kind) ? current.popupsOff.filter((k) => k !== kind) : [...current.popupsOff, kind];
    await this.save({ ...current, popupsOff });
  }

  toggleOpen(): void {
    this.isOpen.update((open) => !open);
    if (this.isOpen()) {
      this.refreshPermission();
    }
  }

  close(): void {
    this.isOpen.set(false);
  }

  setTab(tab: 'list' | 'settings'): void {
    this.tab.set(tab);
  }

  toggleGroup(key: string): void {
    this.expanded.update((open) => {
      const next = new Set(open);
      if (!next.delete(key)) {
        next.add(key);
      }
      return next;
    });
  }

  private async loadOptions(): Promise<void> {
    try {
      const options = await this.gateway?.options();
      if (options) {
        this.options.set(options);
      }
    } catch {
      // The center still lists notifications; the settings show their defaults.
    }
  }

  private currentSettings(): NotificationSettings {
    return this.options()?.settings ?? { paused: false, popupsOff: [] };
  }

  /** Saves at once on screen and on the server; a refusal restores what the server has. */
  private async save(settings: NotificationSettings): Promise<void> {
    const before = this.options();
    this.options.set({ channels: before?.channels ?? [], kinds: before?.kinds ?? [], settings });
    try {
      const saved = await this.gateway?.saveSettings(settings);
      if (saved) {
        this.options.set(saved);
      }
    } catch {
      this.options.set(before);
    }
  }

  private applyRead(ids: readonly number[] | 'all'): void {
    let changed = 0;
    this.items.update((items) =>
      items.map((n) => {
        if (n.read || (ids !== 'all' && !ids.includes(n.id))) {
          return n;
        }
        changed++;
        return { ...n, read: true };
      }),
    );
    this.unread.update((count) => (ids === 'all' ? 0 : Math.max(0, count - changed)));
  }

  private flushPopups(): void {
    this.popupTimer = null;
    const buffered = this.popupBuffer;
    this.popupBuffer = [];
    if (!this.device || this.permission() !== 'granted' || this.device.isVisible()) {
      return;
    }
    const byKind = new Map<NotificationKind, NotificationItem[]>();
    for (const n of buffered) {
      byKind.set(n.kind, [...(byKind.get(n.kind) ?? []), n]);
    }
    for (const [kind, items] of byKind) {
      const ids = items.map((n) => n.id);
      this.device.show(this.popupFor(kind, items), () => this.activate(ids));
    }
  }

  private popupFor(kind: NotificationKind, items: readonly NotificationItem[]): SystemPopup {
    const tag = `pf-${kind}`;
    if (items.length === 1) {
      const n = items[0];
      return { tag, title: this.heading(n), body: `${this.line(n)}\n${n.prTitle}` };
    }
    const newest = [...items].reverse();
    return {
      tag,
      title: this.locale.translate(`notifications.group.${kind}`, { count: items.length }),
      body: newest
        .slice(0, POPUP_TITLES)
        .map((n) => `#${n.prNumber} ${n.prTitle}`)
        .join('\n'),
    };
  }

  private heading(n: NotificationItem): string {
    return this.locale.translate(`notifications.popup.${n.kind}`, { repo: n.repositoryName, pr: n.prNumber });
  }

  private line(n: NotificationItem): string {
    return this.locale.translate(`notifications.reason.${n.reason}`, { actor: n.actorLogin, ...n.details });
  }

  private row(n: NotificationItem): NotificationRowViewModel {
    return {
      id: n.id,
      kind: n.kind,
      reason: n.reason,
      read: n.read,
      heading: this.heading(n),
      line: this.line(n),
      title: n.prTitle,
      createdAt: n.createdAt,
    };
  }
}
