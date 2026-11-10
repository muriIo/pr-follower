import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { ChangedFileCardViewModel } from '../../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';
import { DiffViewerComponent } from '../../../common/diff-viewer/diff-viewer.component';

/** Files changed by a PR, one collapsible card each (same layout as the Dependabot affected code). */
@Component({
  selector: 'app-changed-files',
  standalone: true,
  imports: [DiffViewerComponent, TranslatePipe],
  templateUrl: './changed-files.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ChangedFilesComponent {
  readonly files = input.required<readonly ChangedFileCardViewModel[]>();
  readonly toggleFile = output<number>();
}
