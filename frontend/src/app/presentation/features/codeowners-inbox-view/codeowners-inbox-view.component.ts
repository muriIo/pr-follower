import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { PageHeaderComponent } from '../../common/page-header/page-header.component';
import { InboxPrCardComponent } from './inbox-pr-card/inbox-pr-card.component';

@Component({
  selector: 'app-codeowners-inbox-view',
  standalone: true,
  imports: [PageHeaderComponent, InboxPrCardComponent, TranslatePipe],
  templateUrl: './codeowners-inbox-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CodeownersInboxViewComponent {
  readonly store = inject(WorkspaceStore);
}
