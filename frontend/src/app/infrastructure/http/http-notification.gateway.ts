import { Injectable, inject } from '@angular/core';
import { SessionExpiryMonitor } from '../../application/use-cases/session-expiry.monitor';
import { NotificationGateway } from '../../domain/gateways/notification.gateways';
import { ActionRequestOptions } from '../../domain/gateways/workspace.gateways';
import { ActionError, toActionErrorCode } from '../../domain/models/action-error.models';
import {
  NotificationItem,
  NotificationKind,
  NotificationOptions,
  NotificationPage,
  NotificationReason,
  NotificationSettings,
} from '../../domain/models/notification.models';

export interface ApiNotificationDto {
  readonly id: number;
  readonly kind: string;
  readonly reason: string;
  readonly repository_id: number;
  readonly repository_name?: string;
  readonly pull_request_id: number;
  readonly pr_number: number;
  readonly pr_title?: string;
  readonly actor_login?: string;
  readonly details?: Record<string, string>;
  readonly created_at: string;
  readonly read_at?: string;
  readonly pop_up?: boolean;
}

interface ApiOptionsDto {
  readonly settings?: { readonly paused?: boolean; readonly popups_off?: string[] };
  readonly channels?: { readonly name: string }[];
  readonly kinds?: string[];
}

export function mapNotification(dto: ApiNotificationDto): NotificationItem {
  return {
    id: dto.id,
    kind: dto.kind as NotificationKind,
    reason: dto.reason as NotificationReason,
    repositoryId: dto.repository_id,
    repositoryName: dto.repository_name ?? '',
    pullRequestId: dto.pull_request_id,
    prNumber: dto.pr_number,
    prTitle: dto.pr_title ?? '',
    actorLogin: dto.actor_login ?? '',
    details: dto.details ?? {},
    createdAt: Date.parse(dto.created_at),
    read: !!dto.read_at,
    popUp: dto.pop_up ?? false,
  };
}

function mapOptions(dto: ApiOptionsDto): NotificationOptions {
  return {
    settings: {
      paused: dto.settings?.paused ?? false,
      popupsOff: (dto.settings?.popups_off ?? []) as NotificationKind[],
    },
    channels: (dto.channels ?? []).map((c) => c.name),
    kinds: (dto.kinds ?? []) as NotificationKind[],
  };
}

@Injectable({ providedIn: 'root' })
export class HttpNotificationGateway implements NotificationGateway {
  private readonly session = inject(SessionExpiryMonitor);

  async list(before: number | null, options?: ActionRequestOptions): Promise<NotificationPage> {
    const url = before === null ? '/api/v1/notifications' : `/api/v1/notifications?before=${before}`;
    const res = await this.request<{ notifications?: ApiNotificationDto[]; unread?: number; next_before?: number }>(url, 'GET', undefined, options);
    return {
      items: (res.notifications ?? []).map(mapNotification),
      unread: res.unread ?? 0,
      nextBefore: res.next_before ?? null,
    };
  }

  async markRead(ids: readonly number[] | 'all'): Promise<void> {
    await this.request('/api/v1/notifications/read', 'POST', ids === 'all' ? { all: true } : { ids });
  }

  async options(options?: ActionRequestOptions): Promise<NotificationOptions> {
    return mapOptions(await this.request<ApiOptionsDto>('/api/v1/notifications/settings', 'GET', undefined, options));
  }

  async saveSettings(settings: NotificationSettings): Promise<NotificationOptions> {
    const body = { paused: settings.paused, popups_off: settings.popupsOff };
    return mapOptions(await this.request<ApiOptionsDto>('/api/v1/notifications/settings', 'PUT', body));
  }

  /** One request; a refusal or no answer rejects with an ActionError (ADR-0013). */
  private async request<T>(url: string, method: 'GET' | 'POST' | 'PUT', body?: unknown, options?: ActionRequestOptions): Promise<T> {
    let response: Response;
    try {
      response = await fetch(url, {
        method,
        headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: options?.signal,
      });
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        throw err;
      }
      throw new ActionError('unknown'); // the API itself did not answer
    }
    const payload = (await response.json().catch(() => null)) as (T & { error?: string; code?: string }) | null;
    if (response.status === 401) {
      this.session.report(payload?.code === 'github_token_expired' ? 'github_token_expired' : 'unauthorized');
    }
    if (!response.ok) {
      throw new ActionError(toActionErrorCode(payload?.code), payload?.error ?? '');
    }
    return (payload ?? {}) as T;
  }
}
