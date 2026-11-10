import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { AuthStore } from '../../../application/stores/auth.store';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { CodeownersInboxViewComponent } from '../../features/codeowners-inbox-view/codeowners-inbox-view.component';
import { DependabotRadarViewComponent } from '../../features/dependabot-radar-view/dependabot-radar-view.component';
import { InspectionDrawerComponent } from '../../features/inspection-drawer/inspection-drawer.component';
import { LoginViewComponent } from '../../features/login-view/login-view.component';
import { SeatsViewComponent } from '../../features/seats-view/seats-view.component';
import { TimelineViewComponent } from '../../features/timeline-view/timeline-view.component';
import { SidebarNavComponent } from '../sidebar-nav/sidebar-nav.component';

@Component({
  selector: 'app-workspace-shell',
  standalone: true,
  imports: [
    LoginViewComponent,
    SidebarNavComponent,
    SeatsViewComponent,
    CodeownersInboxViewComponent,
    DependabotRadarViewComponent,
    TimelineViewComponent,
    InspectionDrawerComponent,
  ],
  templateUrl: './workspace-shell.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class WorkspaceShellComponent {
  readonly store = inject(WorkspaceStore);
  readonly authStore = inject(AuthStore);

  constructor() {
    void this.authStore.restore();
  }
}
