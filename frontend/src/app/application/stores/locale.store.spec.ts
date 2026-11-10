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

describe('LocaleStore messages with parameters', () => {
  function store(dictionary: Record<string, string>) {
    TestBed.configureTestingModule({
      providers: [LocaleStore, { provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => dictionary } }],
    });
    return TestBed.inject(LocaleStore);
  }

  it('fills {placeholders} and picks singular or plural forms', async () => {
    const locale = store({
      'seats.openPrs.one': '{count} open PR',
      'seats.openPrs.other': '{count} open PRs',
      'drawer.approvedBy': 'Approved by {login}',
    });
    await locale.switchLocale('en-US');

    expect(locale.translate('drawer.approvedBy', { login: '@ana' })).toBe('Approved by @ana');
    expect(locale.plural('seats.openPrs', 1)).toBe('1 open PR');
    expect(locale.plural('seats.openPrs', 3)).toBe('3 open PRs');
    expect(locale.plural('seats.openPrs', 0)).toBe('0 open PRs');
  });

  it('renders I18nText: literal strings as they are, keys in the current language', async () => {
    const locale = store({ 'drawer.why.owner': 'You own changed files' });
    await locale.switchLocale('en-US');
    expect(locale.text('literal')).toBe('literal');
    expect(locale.text({ key: 'drawer.why.owner' })).toBe('You own changed files');
    expect(locale.text(undefined)).toBe('');
  });
});
