import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { LocaleStore } from '../../application/stores/locale.store';
import { WorkspaceStore } from '../../application/stores/workspace.store';
import { TranslatePipe } from '../../infrastructure/i18n/json-translation.gateway';

@Component({
  selector: 'app-workspace-shell',
  standalone: true,
  imports: [TranslatePipe],
  templateUrl: './workspace-shell.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class WorkspaceShellComponent {
  readonly store = inject(WorkspaceStore);
  readonly localeStore = inject(LocaleStore);

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

  readonly currentTitle = computed(() => {
    const v = this.store.view();
    if (v === 'seats') return this.localeStore.translate('seats.title');
    if (v === 'inbox') return this.localeStore.translate('inbox.title');
    if (v === 'radar') return this.localeStore.translate('radar.title');
    return this.localeStore.translate('timeline.title');
  });

  readonly currentSubtitle = computed(() => {
    const v = this.store.view();
    if (v === 'seats') return this.localeStore.translate('seats.subtitle');
    if (v === 'inbox') return '';
    if (v === 'radar') {
      const deps = this.store.openDependabotPrs();
      const repoCount = new Set(deps.map((p) => p.repo)).size;
      return `${deps.length} PRs abertos em ${repoCount} repositórios, agrupados por risco.`;
    }
    return this.localeStore.translate('timeline.subtitle');
  });

  toggleLocale(): void {
    const next = this.localeStore.locale() === 'pt-BR' ? 'en-US' : 'pt-BR';
    void this.localeStore.switchLocale(next);
  }
}
