import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { PageHeaderComponent } from '../../common/page-header/page-header.component';
import { StatusDotComponent } from '../../common/status-dot/status-dot.component';

@Component({
  selector: 'app-timeline-view',
  standalone: true,
  imports: [PageHeaderComponent, StatusDotComponent, TranslatePipe],
  templateUrl: './timeline-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class TimelineViewComponent {
  readonly store = inject(WorkspaceStore);
}
