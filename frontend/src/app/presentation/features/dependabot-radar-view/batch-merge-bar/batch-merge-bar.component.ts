import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { ActionViewModel } from '../../../../application/use-cases/action-cancellation.manager';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';

export interface SafeBatchSummaryViewModel {
  readonly has: boolean;
  readonly title: string;
  readonly canApprove: boolean;
  readonly approveLabel: string;
  readonly mergeDis: string;
  readonly mergeCls: string;
  readonly mergeLabel: string;
}

@Component({
  selector: 'app-batch-merge-bar',
  standalone: true,
  imports: [TranslatePipe],
  templateUrl: './batch-merge-bar.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class BatchMergeBarComponent {
  readonly summary = input.required<SafeBatchSummaryViewModel>();
  readonly batchAction = input.required<ActionViewModel>();
  readonly batchApprove = output<void>();
  readonly batchMerge = output<void>();
}
