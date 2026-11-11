import { ChangeDetectionStrategy, Component, input, signal } from '@angular/core';
import { DrawerChecksViewModel } from '../../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../../infrastructure/i18n/json-translation.gateway';

/** GitHub's checks panel: a verdict and counts, then the checks grouped by outcome, each group collapsible. */
@Component({
  selector: 'app-checks-panel',
  standalone: true,
  imports: [TranslatePipe],
  templateUrl: './checks-panel.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ChecksPanelComponent {
  readonly checks = input.required<DrawerChecksViewModel>();
  /** Groups the user opened or closed; the others follow their default (failing and running open). */
  private readonly toggled = signal<Readonly<Record<string, boolean>>>({});

  isOpen(key: string, byDefault: boolean): boolean {
    return this.toggled()[key] ?? byDefault;
  }

  toggle(key: string, byDefault: boolean): void {
    this.toggled.update((t) => ({ ...t, [key]: !(t[key] ?? byDefault) }));
  }
}
