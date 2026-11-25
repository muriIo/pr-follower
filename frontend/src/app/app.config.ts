import { ApplicationConfig, Provider, inject, provideAppInitializer, provideBrowserGlobalErrorListeners } from '@angular/core';
import { provideRouter } from '@angular/router';
import { routes } from './app.routes';
import { LocaleStore } from './application/stores/locale.store';
import {
  AUTH_GATEWAY,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  REPOSITORY_IMPORT_GATEWAY,
  TRANSLATION_GATEWAY,
} from './domain/gateways/workspace.gateways';
import { NOTIFICATION_DEVICE, NOTIFICATION_GATEWAY } from './domain/gateways/notification.gateways';
import { HttpAuthGateway } from './infrastructure/auth/http-auth.gateway';
import { BrowserNotificationDevice } from './infrastructure/browser/browser-notification-device';
import { HttpNotificationGateway } from './infrastructure/http/http-notification.gateway';
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
    { provide: NOTIFICATION_GATEWAY, useExisting: HttpNotificationGateway },
    { provide: NOTIFICATION_DEVICE, useExisting: BrowserNotificationDevice },
  ];
}

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(routes),
    ...provideAppGateways(),
    // The first render needs the whole dictionary: the built-in one covers only part of the UI.
    provideAppInitializer(() => inject(LocaleStore).load()),
  ],
};
