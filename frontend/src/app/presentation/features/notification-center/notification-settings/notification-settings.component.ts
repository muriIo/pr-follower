import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { NotificationStore } from '../../../../application/stores/notification.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';

/** The browser permission with the one action it allows, the pause, and a switch per kind. */
@Component({
  selector: 'app-notification-settings',
  standalone: true,
  imports: [TranslatePipe],
  templateUrl: './notification-settings.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NotificationSettingsComponent {
  readonly store = inject(NotificationStore);
}
