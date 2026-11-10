import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { AuthStore } from '../../../application/stores/auth.store';
import { LocaleStore } from '../../../application/stores/locale.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { StatusDotComponent } from '../../common/status-dot/status-dot.component';

/** GitHub is the only way in (ADR-0010): no credentials are typed or stored here. */
@Component({
  selector: 'app-login-view',
  standalone: true,
  imports: [StatusDotComponent, TranslatePipe],
  templateUrl: './login-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class LoginViewComponent {
  readonly authStore = inject(AuthStore);
  readonly localeStore = inject(LocaleStore);

  readonly errorKey = computed(() => {
    const error = this.authStore.authError();
    return error ? `auth.error.${error}` : null;
  });
  readonly redirecting = computed(() => this.authStore.status() === 'redirecting');

  toggleLocale(): void {
    const next = this.localeStore.locale() === 'pt-BR' ? 'en-US' : 'pt-BR';
    void this.localeStore.switchLocale(next);
  }

  signIn(): void {
    this.authStore.signIn();
  }
}
