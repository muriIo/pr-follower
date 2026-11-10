import { ApplicationConfig, Provider, provideBrowserGlobalErrorListeners } from '@angular/core';
import { provideRouter } from '@angular/router';
import { routes } from './app.routes';
import {
  AUTH_GATEWAY,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  REPOSITORY_IMPORT_GATEWAY,
  TRANSLATION_GATEWAY,
} from './domain/gateways/workspace.gateways';
import { HttpAuthGateway } from './infrastructure/auth/http-auth.gateway';
import { HttpPullRequestGateway } from './infrastructure/http/http-pull-request.gateway';
import { HttpRepositoryImportGateway } from './infrastructure/http/http-repository-import.gateway';
import { JsonTranslationGateway } from './infrastructure/i18n/json-translation.gateway';
import { SseLiveEventGateway } from './infrastructure/sse/sse-live-event.gateway';

export function provideAppGateways(): Provider[] {
  return [
    { provide: PULL_REQUEST_GATEWAY, useExisting: HttpPullRequestGateway },
    { provide: LIVE_EVENT_GATEWAY, useExisting: SseLiveEventGateway },
    { provide: TRANSLATION_GATEWAY, useExisting: JsonTranslationGateway },
    { provide: AUTH_GATEWAY, useExisting: HttpAuthGateway },
    { provide: REPOSITORY_IMPORT_GATEWAY, useExisting: HttpRepositoryImportGateway },
  ];
}

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(routes),
    ...provideAppGateways(),
  ],
};
