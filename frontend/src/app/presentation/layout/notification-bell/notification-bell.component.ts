import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { LocaleStore } from '../../../application/stores/locale.store';
import { NotificationStore } from '../../../application/stores/notification.store';

/** Opens the notification center; its badge counts the unread notifications (9+ above nine). */
@Component({
  selector: 'app-notification-bell',
  standalone: true,
  templateUrl: './notification-bell.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NotificationBellComponent {
  readonly store = inject(NotificationStore);
  private readonly locale = inject(LocaleStore);

  readonly label = computed(() => {
    const unread = this.store.unread();
    return unread > 0
      ? this.locale.translate('notifications.bell.unread', { count: unread })
      : this.locale.translate('notifications.bell');
  });
}
