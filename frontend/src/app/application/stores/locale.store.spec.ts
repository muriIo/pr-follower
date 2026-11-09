import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { TRANSLATION_GATEWAY, TranslationGateway } from '../../domain/gateways/workspace.gateways';
import { LocaleStore } from './locale.store';

describe('LocaleStore (Signal-based i18n Store)', () => {
  it('defaults to pt-BR and switches dynamically to en-US via TranslationGateway', async () => {
    const fakeGateway: TranslationGateway = {
      loadDictionary: async (locale: string) =>
        locale === 'en-US'
          ? { 'nav.inbox': 'My Turn (CODEOWNERS)', 'seats.sync': 'Sync' }
          : { 'nav.inbox': 'Minha Vez (CODEOWNERS)', 'seats.sync': 'Sincronizar' },
    };

    TestBed.configureTestingModule({
      providers: [LocaleStore, { provide: TRANSLATION_GATEWAY, useValue: fakeGateway }],
    });

    const store = TestBed.inject(LocaleStore);
    expect(store.locale()).toBe('pt-BR');
    expect(store.translate('nav.inbox')).toBe('Minha Vez (CODEOWNERS)');

    await store.switchLocale('en-US');
    expect(store.locale()).toBe('en-US');
    expect(store.translate('nav.inbox')).toBe('My Turn (CODEOWNERS)');
  });
});
