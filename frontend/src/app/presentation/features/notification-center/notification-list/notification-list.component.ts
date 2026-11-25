import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { LocaleStore } from '../../../../application/stores/locale.store';
import {
  NotificationGroupViewModel,
  NotificationRowViewModel,
  NotificationStore,
} from '../../../../application/stores/notification.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';

/** Each kind and reason has its own mark, so a glance tells a review request from a merge. */
const ICONS: Readonly<Record<string, string>> = {
  review_requested: '👀',
  new_commit: '🔁',
  snooze_ended: '⏰',
  author_replied: '💬',
  high_risk: '🔴',
  merged: '🟣',
  closed: '⚪',
  approval_dismissed: '↩️',
  ci_failed: '❌',
};

const UNITS: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ['day', 86_400_000],
  ['hour', 3_600_000],
  ['minute', 60_000],
];

/** The notifications, unread bursts first, each burst of one kind as one entry. */
@Component({
  selector: 'app-notification-list',
  standalone: true,
  imports: [TranslatePipe],
  templateUrl: './notification-list.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NotificationListComponent {
  readonly store = inject(NotificationStore);
  private readonly locale = inject(LocaleStore);

  readonly ordered = computed(() => {
    const groups = this.store.groups();
    return [...groups.filter((g) => g.unread > 0), ...groups.filter((g) => g.unread === 0)];
  });

  markAll(): void {
    if (this.store.unread() > 0) {
      void this.store.markAllRead();
    }
  }

  icon(row: Pick<NotificationRowViewModel, 'reason'>): string {
    return ICONS[row.reason] ?? '•';
  }

  ids(group: NotificationGroupViewModel): number[] {
    return group.items.map((n) => n.id);
  }

  ago(at: number): string {
    const format = new Intl.RelativeTimeFormat(this.locale.locale(), { numeric: 'auto' });
    const elapsed = at - Date.now();
    for (const [unit, ms] of UNITS) {
      if (Math.abs(elapsed) >= ms) {
        return format.format(Math.round(elapsed / ms), unit);
      }
    }
    return format.format(0, 'minute');
  }
}
