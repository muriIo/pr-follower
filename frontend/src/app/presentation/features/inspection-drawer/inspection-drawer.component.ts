import { ChangeDetectionStrategy, Component, inject, input } from '@angular/core';
import { DrawerViewModel, WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { CancellableActionComponent } from '../../common/cancellable-action/cancellable-action.component';
import { DiffViewerComponent } from '../../common/diff-viewer/diff-viewer.component';
import { RiskBadgeComponent } from '../../common/risk-badge/risk-badge.component';
import { StatusDotComponent } from '../../common/status-dot/status-dot.component';
import { DependabotImpactComponent } from './dependabot-impact/dependabot-impact.component';

@Component({
  selector: 'app-inspection-drawer',
  standalone: true,
  imports: [
    StatusDotComponent,
    RiskBadgeComponent,
    CancellableActionComponent,
    DiffViewerComponent,
    DependabotImpactComponent,
    TranslatePipe,
  ],
  templateUrl: './inspection-drawer.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class InspectionDrawerComponent {
  readonly store = inject(WorkspaceStore);
  readonly drawer = input.required<DrawerViewModel>();
}
