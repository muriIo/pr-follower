import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { RadarCardViewModel } from '../../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';
import { RiskBadgeComponent } from '../../../common/risk-badge/risk-badge.component';

@Component({
  selector: 'app-radar-pr-card',
  standalone: true,
  imports: [RiskBadgeComponent, TranslatePipe],
  templateUrl: './radar-pr-card.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RadarPrCardComponent {
  readonly card = input.required<RadarCardViewModel>();
  readonly selected = output<number>();
}
