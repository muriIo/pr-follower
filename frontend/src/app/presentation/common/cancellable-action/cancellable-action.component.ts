import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { ActionViewModel } from '../../../application/use-cases/action-cancellation.manager';

@Component({
  selector: 'app-cancellable-action',
  standalone: true,
  templateUrl: './cancellable-action.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CancellableActionComponent {
  readonly action = input.required<ActionViewModel>();
  readonly cancelLabel = input<string>('Cancelar');
  readonly mode = input<'header' | 'inline-sm' | 'drawer'>('header');
  readonly cancelButtonId = input<string>('');
  readonly showNote = input<boolean>(true);
}
