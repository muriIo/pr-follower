import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { SeatCardViewModel } from '../../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';
import { CancellableActionComponent } from '../../../common/cancellable-action/cancellable-action.component';
import { RiskBadgeComponent } from '../../../common/risk-badge/risk-badge.component';
import { StatusDotComponent } from '../../../common/status-dot/status-dot.component';

@Component({
  selector: 'app-seat-card',
  standalone: true,
  imports: [StatusDotComponent, RiskBadgeComponent, CancellableActionComponent, TranslatePipe],
  templateUrl: './seat-card.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SeatCardComponent {
  readonly seat = input.required<SeatCardViewModel>();
  readonly openRadar = output<void>();
  /** The user wants to restore the GitHub App's access to this repository. */
  readonly fixAccess = output<void>();
}
