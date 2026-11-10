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
  'seats.followRepo': '+ Importar repositório',
  'seats.access.removed': 'Sem acesso: o GitHub App foi desinstalado ou o repositório foi removido dele.',
  'seats.access.suspended': 'Acesso suspenso: a instalação do GitHub App foi suspensa.',
  'seats.access.fix': 'Corrigir acesso',
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
  'auth.title': 'Entre para acompanhar seus PRs',
  'auth.lead': 'Use sua conta do GitHub. Você verá os repositórios onde o app PR Follower está instalado.',
  'auth.signInWithGitHub': 'Entrar com GitHub',
  'auth.redirecting': 'Redirecionando para o GitHub...',
  'auth.noPasswords': 'Não guardamos senhas: o acesso é sempre autorizado pelo GitHub.',
  'auth.logoutTitle': 'Encerrar sessão',
  'auth.error.not_configured': 'O login com GitHub ainda não foi configurado neste servidor.',
  'auth.error.state_mismatch': 'A tentativa de login expirou ou não pôde ser verificada. Tente novamente.',
  'auth.error.access_denied': 'Você cancelou a autorização no GitHub.',
  'auth.error.login_failed': 'O GitHub não confirmou o login. Tente novamente.',
  'auth.error.unknown': 'Não foi possível entrar. Tente novamente.',
  'auth.logout': 'Sair',
  'import.title': 'Importar repositório',
  'import.subtitle': 'Escolha uma conta onde o app PR Follower está instalado e siga os repositórios que você quer acompanhar.',
  'import.back': '← Voltar aos Seats',
  'import.account': 'Conta do GitHub',
  'import.search': 'Buscar repositórios...',
  'import.private': 'Privado',
  'import.follow': 'Seguir',
  'import.following': 'Seguindo',
  'import.loadingAccounts': 'Carregando suas contas do GitHub...',
  'import.loadingRepositories': 'Carregando repositórios...',
  'import.noMatches': 'Nenhum repositório encontrado.',
  'import.noInstallations.title': 'Instale o PR Follower no GitHub',
  'import.noInstallations.body': 'O app ainda não está instalado em nenhuma conta que você acessa. Instale-o na sua conta ou organização e escolha os repositórios.',
  'import.install': 'Instalar no GitHub',
  'import.missingRepo': 'Não encontrou um repositório?',
  'import.adjustPermissions': 'Ajustar permissões do GitHub App →',
  'import.addAccount': '+ Adicionar conta do GitHub',
  'import.error.unauthorized': 'Sua sessão do GitHub expirou. Entre novamente.',
  'import.error.not_found': 'O GitHub não confirmou o acesso a esse repositório. Ajuste as permissões do app.',
  'import.error.not_configured': 'A importação de repositórios não está configurada neste servidor.',
  'import.error.failed': 'Não foi possível falar com o GitHub. Tente novamente.',
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
