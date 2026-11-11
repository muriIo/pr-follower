import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { DrawerReviewViewModel } from '../../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';
import { RiskBadgeComponent } from '../../../common/risk-badge/risk-badge.component';

/** Who reviewed the PR and what each reviewer decided; a review of older code is marked. */
@Component({
  selector: 'app-review-list',
  standalone: true,
  imports: [RiskBadgeComponent, TranslatePipe],
  templateUrl: './review-list.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ReviewListComponent {
  readonly reviews = input.required<readonly DrawerReviewViewModel[]>();
}
