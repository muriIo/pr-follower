import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LocaleStore } from '../../application/stores/locale.store';
import { JsonTranslationGateway, TranslatePipe } from './json-translation.gateway';

describe('JsonTranslationGateway & TranslatePipe', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('loads JSON dictionary over fetch and translates keys via TranslatePipe', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ 'nav.inbox': 'Minha Vez (CODEOWNERS)' }), { status: 200 }),
    );

    TestBed.configureTestingModule({
      providers: [JsonTranslationGateway, LocaleStore, TranslatePipe],
    });

    const gateway = TestBed.inject(JsonTranslationGateway);
    const dict = await gateway.loadDictionary('pt-BR');
    expect(dict['nav.inbox']).toBe('Minha Vez (CODEOWNERS)');

    const pipe = TestBed.inject(TranslatePipe);
    expect(pipe.transform('nav.inbox')).toBe('Minha Vez (CODEOWNERS)');
  });
});
