import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { ActionViewModel } from '../../../application/use-cases/action-cancellation.manager';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';

@Component({
  selector: 'app-cancellable-action',
  standalone: true,
  imports: [TranslatePipe],
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
