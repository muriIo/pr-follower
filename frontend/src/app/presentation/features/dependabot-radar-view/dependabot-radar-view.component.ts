import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { PageHeaderComponent } from '../../common/page-header/page-header.component';
import { BatchMergeBarComponent } from './batch-merge-bar/batch-merge-bar.component';
import { RadarPrCardComponent } from './radar-pr-card/radar-pr-card.component';

@Component({
  selector: 'app-dependabot-radar-view',
  standalone: true,
  imports: [PageHeaderComponent, BatchMergeBarComponent, RadarPrCardComponent, TranslatePipe],
  templateUrl: './dependabot-radar-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class DependabotRadarViewComponent {
  readonly store = inject(WorkspaceStore);

  readonly subtitle = computed(() => {
    const deps = this.store.openDependabotPrs();
    const repoCount = new Set(deps.map((p) => p.repo)).size;
    return `${deps.length} PRs abertos em ${repoCount} repositórios, agrupados por risco.`;
  });
}
