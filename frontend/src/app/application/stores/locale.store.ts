import { Injectable, inject, signal } from '@angular/core';
import { TRANSLATION_GATEWAY } from '../../domain/gateways/workspace.gateways';

const DEFAULT_PT_BR_DICTIONARY: Readonly<Record<string, string>> = {
  'app.brand': 'PR Follower',
  'app.perspectives': 'Perspectivas',
  'nav.inbox': 'Minha Vez (CODEOWNERS)',
  'nav.radar': 'Radar Dependabot',
  'nav.seats': 'Seats de Repositórios',
  'nav.timeline': 'Linha do Tempo',
  'sse.connected': 'Ao vivo (SSE)',
  'sse.reconnecting': 'Reconectando (SSE)',
  'sse.disconnected': 'Desconectado (SSE)',
  'seats.title': 'Seats de Repositórios',
  'seats.subtitle': 'Monitoramento em tempo real dos repositórios seguidos',
  'seats.sync': 'Sincronizar',
  'seats.followRepo': '+ Seguir Repositório',
  'seats.filter.all': 'Todos',
  'seats.filter.turn': 'Com PRs na Minha Vez',
  'seats.filter.deps': 'Dependabot Pendente',
  'seats.ci.stable': 'CI Estável',
  'seats.ci.failing': 'CI Falhando',
  'seats.ci.running': 'CI Rodando',
  'seats.kpi.yourTurn': 'Sua Vez',
  'seats.kpi.dependabot': 'Dependabot',
  'seats.kpi.openPrs': 'PRs Abertos',
  'seats.empty': 'Nada aberto. Repositório em dia.',
  'inbox.title': 'Minha Vez (CODEOWNERS)',
  'inbox.toggleMuted': 'Mostrar PRs silenciados / aguardando autor',
  'inbox.waitingSection': 'Aguardando Sua Revisão',
  'inbox.mutedSection': 'Silenciados pelo Gate ou Aprovados pelo Time',
  'inbox.empty': 'Você está em dia. Nenhum PR espera sua ação.',
  'inbox.noConflicts': 'Sem conflitos',
  'radar.title': 'Radar Dependabot',
  'radar.empty': 'Nenhum PR do Dependabot aberto.',
  'radar.group.high': '🔴 Alto Risco — Requer Análise de Impacto',
  'radar.group.low': '🟢 Baixo Risco — Seguros para Merge Rápido',
  'radar.group.medium': '🟡 Médio Risco — Revisar Antes de Mesclar',
  'radar.batch.cancel': 'Cancelar Lote',
  'timeline.title': 'Linha do Tempo',
  'timeline.subtitle': 'Eventos recebidos por SSE, do mais recente ao mais antigo.',
  'timeline.pause': 'Pausar',
  'timeline.resume': 'Retomar',
  'timeline.now': 'Agora',
  'timeline.lastHour': 'Última hora',
  'timeline.earlierToday': 'Hoje cedo',
  'drawer.openGithub': 'Abrir no GitHub ↗',
  'drawer.close': 'Fechar inspeção',
  'drawer.signals': 'Sinais',
  'drawer.depTree': 'Árvore de Dependência',
  'drawer.blastRadius': 'Raio de Impacto no Código',
  'drawer.noBlast': 'Nenhum import deste pacote no seu código de produção.',
  'drawer.breakingNotes': 'Notas de Quebra & CVEs',
  'drawer.approveBeforeMerge': 'Aprove antes de mesclar.',
  'drawer.approve': 'Aprovar',
  'drawer.approvedByYou': 'Aprovado por você',
  'drawer.mergeSquash': 'Mesclar via squash',
  'drawer.cancelEsc': 'Cancelar (Esc)',
  'drawer.cancel': 'Cancelar',
  'drawer.rebase': '@dependabot rebase',
  'drawer.recreate': '@dependabot recreate',
  'drawer.silence': 'Silenciar',
  'drawer.silenceCommit': 'Silenciar até próximo commit',
  'drawer.silenceCi': 'Silenciar até CI passar',
  'drawer.passToTeammate': 'Passar para colega',
  'drawer.resize': 'Redimensionar painel',
  'auth.subtitle': 'Triagem baseada em turno para CODEOWNERS e análise de impacto zero-clone para Dependabot.',
  'auth.loginLabel': 'Usuário GitHub',
  'auth.teamLabel': 'Time CODEOWNERS',
  'auth.tokenLabel': 'Token GitHub (Opcional)',
  'auth.submitGithub': 'Entrar com GitHub',
  'auth.quickSeats': 'Acesso Rápido (Seats do Time)',
  'auth.authenticating': 'Autenticando no GitHub...',
  'auth.logout': 'Sair',
};

@Injectable({ providedIn: 'root' })
export class LocaleStore {
  private readonly gateway = inject(TRANSLATION_GATEWAY, { optional: true });

  readonly locale = signal<'pt-BR' | 'en-US'>('pt-BR');
  readonly dictionary = signal<Record<string, string>>({ ...DEFAULT_PT_BR_DICTIONARY });

  translate(key: string): string {
    return this.dictionary()[key] ?? DEFAULT_PT_BR_DICTIONARY[key] ?? key;
  }

  async switchLocale(nextLocale: 'pt-BR' | 'en-US'): Promise<void> {
    if (this.gateway) {
      const loaded = await this.gateway.loadDictionary(nextLocale);
      this.dictionary.set(loaded);
    }
    this.locale.set(nextLocale);
  }
}
