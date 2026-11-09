import { ApplicationConfig, Provider, provideBrowserGlobalErrorListeners } from '@angular/core';
import { provideRouter } from '@angular/router';
import { routes } from './app.routes';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from './domain/gateways/workspace.gateways';
import { HttpPullRequestGateway } from './infrastructure/http/http-pull-request.gateway';
import { JsonTranslationGateway } from './infrastructure/i18n/json-translation.gateway';
import { SseLiveEventGateway } from './infrastructure/sse/sse-live-event.gateway';

export function provideAppGateways(): Provider[] {
  return [
    { provide: PULL_REQUEST_GATEWAY, useExisting: HttpPullRequestGateway },
    { provide: LIVE_EVENT_GATEWAY, useExisting: SseLiveEventGateway },
    { provide: TRANSLATION_GATEWAY, useExisting: JsonTranslationGateway },
  ];
}

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(routes),
    ...provideAppGateways(),
  ],
};
