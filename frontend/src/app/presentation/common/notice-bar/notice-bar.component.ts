import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';

/** A one-line, dismissible explanation of something that changed under the user. */
@Component({
  selector: 'app-notice-bar',
  standalone: true,
  imports: [TranslatePipe],
  templateUrl: './notice-bar.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NoticeBarComponent {
  readonly text = input.required<string>();
  readonly dismissed = output<void>();
}
