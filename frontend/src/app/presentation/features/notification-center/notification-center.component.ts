import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { NotificationStore } from '../../../application/stores/notification.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { NotificationListComponent } from './notification-list/notification-list.component';
import { NotificationSettingsComponent } from './notification-settings/notification-settings.component';

/** The notification center: a side panel over the workspace with the list and the settings. */
@Component({
  selector: 'app-notification-center',
  standalone: true,
  imports: [NotificationListComponent, NotificationSettingsComponent, TranslatePipe],
  templateUrl: './notification-center.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NotificationCenterComponent {
  readonly store = inject(NotificationStore);
}
