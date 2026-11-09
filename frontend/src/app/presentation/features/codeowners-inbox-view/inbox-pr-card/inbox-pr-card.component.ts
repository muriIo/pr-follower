import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { InboxCardViewModel } from '../../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';
import { RiskBadgeComponent } from '../../../common/risk-badge/risk-badge.component';

@Component({
  selector: 'app-inbox-pr-card',
  standalone: true,
  imports: [RiskBadgeComponent, TranslatePipe],
  templateUrl: './inbox-pr-card.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class InboxPrCardComponent {
  readonly card = input.required<InboxCardViewModel>();
  readonly muted = input<boolean>(false);
  readonly selected = output<number>();
}
