import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { DrawerViewModel } from '../../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';
import { DiffViewerComponent } from '../../../common/diff-viewer/diff-viewer.component';
import { RiskBadgeComponent } from '../../../common/risk-badge/risk-badge.component';

@Component({
  selector: 'app-dependabot-impact',
  standalone: true,
  imports: [DiffViewerComponent, RiskBadgeComponent, TranslatePipe],
  templateUrl: './dependabot-impact.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class DependabotImpactComponent {
  readonly drawer = input.required<DrawerViewModel>();
  readonly selectTreeNode = output<number>();
  readonly toggleBlastFile = output<number>();
  readonly showAllBlastFiles = output<void>();
  readonly toggleAllBlastFiles = output<void>();
}
