import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { ActivityIconComponent } from '../../common/activity-icon/activity-icon.component';
import { PageHeaderComponent } from '../../common/page-header/page-header.component';

@Component({
  selector: 'app-timeline-view',
  standalone: true,
  imports: [PageHeaderComponent, ActivityIconComponent, TranslatePipe],
  templateUrl: './timeline-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class TimelineViewComponent {
  readonly store = inject(WorkspaceStore);

  onRepoChange(event: Event): void {
    this.store.setTimelineRepo((event.target as HTMLSelectElement).value || null);
  }
}
