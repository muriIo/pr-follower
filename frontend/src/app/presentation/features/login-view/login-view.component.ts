import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { AuthStore } from '../../../application/stores/auth.store';
import { LocaleStore } from '../../../application/stores/locale.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { CancellableActionComponent } from '../../common/cancellable-action/cancellable-action.component';
import { StatusDotComponent } from '../../common/status-dot/status-dot.component';

@Component({
  selector: 'app-login-view',
  standalone: true,
  imports: [CancellableActionComponent, StatusDotComponent, TranslatePipe],
  templateUrl: './login-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class LoginViewComponent {
  readonly authStore = inject(AuthStore);
  readonly localeStore = inject(LocaleStore);

  readonly loginHandle = signal('murilo');
  readonly teamHandle = signal('@arvore/frontend');

  readonly presets = [
    { login: 'murilo', team: '@arvore/frontend' },
    { login: 'bianca-dev', team: '@arvore/core-architects' },
  ] as const;

  onHandleInput(event: Event): void {
    const input = event.target as HTMLInputElement;
    this.loginHandle.set(input.value);
  }

  onTeamInput(event: Event): void {
    const input = event.target as HTMLInputElement;
    this.teamHandle.set(input.value);
  }

  selectPreset(login: string, team: string): void {
    this.loginHandle.set(login);
    this.teamHandle.set(team);
  }

  toggleLocale(): void {
    const next = this.localeStore.locale() === 'pt-BR' ? 'en-US' : 'pt-BR';
    void this.localeStore.switchLocale(next);
  }

  onSubmit(event: Event): void {
    event.preventDefault();
    void this.authStore.login({
      login: this.loginHandle(),
      team: this.teamHandle(),
    });
  }
}
