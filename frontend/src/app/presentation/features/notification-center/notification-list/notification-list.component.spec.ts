import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { LocaleStore } from '../../../../application/stores/locale.store';
import { NotificationStore } from '../../../../application/stores/notification.store';
import { NotificationItem } from '../../../../domain/models/notification.models';
import { NotificationListComponent } from './notification-list.component';

const NOW = Date.now();

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
    createdAt: NOW - (10 - id) * 1000,
    read: false,
    popUp: false,
    ...patch,
  };
}

function render(items: NotificationItem[]) {
  TestBed.configureTestingModule({ imports: [NotificationListComponent] });
  const store = TestBed.inject(NotificationStore);
  store.items.set(items);
  store.unread.set(items.filter((n) => !n.read).length);
  const fixture = TestBed.createComponent(NotificationListComponent);
  fixture.detectChanges();
  return { el: fixture.nativeElement as HTMLElement, store, fixture, t: TestBed.inject(LocaleStore) };
}

describe('NotificationListComponent', () => {
  it('says when there is nothing', () => {
    const { el, t } = render([]);
    expect(el.textContent).toContain(t.translate('notifications.empty'));
  });

  it('gives each kind its own look and puts unread entries first', () => {
    const { el } = render([
      item(1, { kind: 'involved', reason: 'merged', read: true, createdAt: NOW }),
      item(2, { kind: 'risk', reason: 'high_risk', details: { package: 'lodash', from: '4', to: '5' }, createdAt: NOW - 600_000 }),
    ]);
    const entries = [...el.querySelectorAll('.pf-notif-item')];
    expect(entries.map((e) => e.classList.contains('pf-notif-risk'))).toEqual([true, false]);
    expect(entries[1].classList).toContain('pf-notif-involved');
    expect(entries[0].textContent).toContain('🔴');
    expect(entries[0].textContent).toContain('lodash 4 → 5');
    expect(entries[0].querySelector('.pf-notif-dot')).not.toBeNull();
    expect(entries[1].querySelector('.pf-notif-dot')).toBeNull();
  });

  it('shows a burst as one entry that opens all of it and expands', () => {
    const { el, store, fixture, t } = render([item(3), item(2), item(1)]);
    const activate = vi.spyOn(store, 'activate').mockImplementation(() => undefined);
    const entry = el.querySelector('.pf-notif-item')!;
    expect(entry.textContent).toContain(t.translate('notifications.group.turn', { count: 3 }));

    (entry.querySelector('.pf-notif-open') as HTMLButtonElement).click();
    expect(activate).toHaveBeenCalledWith([3, 2, 1]);

    (entry.querySelector('.pf-notif-expand') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el.querySelectorAll('.pf-notif-sublist li')).toHaveLength(3);
  });

  it('marks everything read', () => {
    const { el, store } = render([item(1)]);
    const markAll = vi.spyOn(store, 'markAllRead').mockResolvedValue();
    (el.querySelector('.pf-notif-actions button') as HTMLButtonElement).click();
    expect(markAll).toHaveBeenCalled();
  });
});
