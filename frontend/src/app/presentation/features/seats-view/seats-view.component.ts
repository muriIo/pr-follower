import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { CancellableActionComponent } from '../../common/cancellable-action/cancellable-action.component';
import { PageHeaderComponent } from '../../common/page-header/page-header.component';
import { SeatCardComponent } from './seat-card/seat-card.component';

@Component({
  selector: 'app-seats-view',
  standalone: true,
  imports: [PageHeaderComponent, CancellableActionComponent, SeatCardComponent, TranslatePipe],
  templateUrl: './seats-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SeatsViewComponent {
  readonly store = inject(WorkspaceStore);
}
