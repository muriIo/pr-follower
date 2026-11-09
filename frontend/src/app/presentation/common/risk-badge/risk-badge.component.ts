import { ChangeDetectionStrategy, Component, input } from '@angular/core';

@Component({
  selector: 'app-risk-badge',
  standalone: true,
  templateUrl: './risk-badge.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RiskBadgeComponent {
  readonly label = input.required<string>();
  readonly badgeClass = input<string>('pf-tag');
  readonly tooltip = input<string>('');
}
