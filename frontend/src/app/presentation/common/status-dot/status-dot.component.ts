import { ChangeDetectionStrategy, Component, input } from '@angular/core';

@Component({
  selector: 'app-status-dot',
  standalone: true,
  templateUrl: './status-dot.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class StatusDotComponent {
  readonly dotClass = input.required<string>();
}
