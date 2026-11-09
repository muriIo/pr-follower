import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { AuthStore } from '../../../application/stores/auth.store';
import { LocaleStore } from '../../../application/stores/locale.store';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { createDefaultAuthenticatedUser } from '../../../domain/models/workspace.models';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { StatusDotComponent } from '../../common/status-dot/status-dot.component';

@Component({
  selector: 'app-sidebar-nav',
  standalone: true,
  imports: [StatusDotComponent, TranslatePipe],
  templateUrl: './sidebar-nav.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SidebarNavComponent {
  readonly store = inject(WorkspaceStore);
  readonly authStore = inject(AuthStore);
  readonly localeStore = inject(LocaleStore);

  readonly currentUser = computed(
    () => this.authStore.user() ?? createDefaultAuthenticatedUser(),
  );

  readonly connectionDotClass = computed(() => {
    const st = this.store.connectionState();
    if (st === 'conectado') return 'pf-dot pf-dot-ok pf-dot-pulse';
    if (st === 'reconectando') return 'pf-dot pf-dot-warn pf-dot-pulse';
    return 'pf-dot pf-dot-fail';
  });

  readonly connectionLabelKey = computed(() => {
    const st = this.store.connectionState();
    if (st === 'conectado') return 'sse.connected';
    if (st === 'reconectando') return 'sse.reconnecting';
    return 'sse.disconnected';
  });

  toggleLocale(): void {
    const next = this.localeStore.locale() === 'pt-BR' ? 'en-US' : 'pt-BR';
    void this.localeStore.switchLocale(next);
  }
}
