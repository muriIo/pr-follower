import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { RenderedDiffLine } from '../../../application/stores/workspace.store';

@Component({
  selector: 'app-diff-viewer',
  standalone: true,
  templateUrl: './diff-viewer.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class DiffViewerComponent {
  readonly file = input<string>('');
  readonly showHeader = input<boolean>(true);
  readonly deltaAdd = input<string | undefined>(undefined);
  readonly deltaDel = input<string | undefined>(undefined);
  readonly lines = input.required<readonly RenderedDiffLine[]>();
}
