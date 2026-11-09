import { Injectable, Pipe, PipeTransform, inject } from '@angular/core';
import { LocaleStore } from '../../application/stores/locale.store';
import { TranslationGateway } from '../../domain/gateways/workspace.gateways';
import { retryWithJitter } from '../resilience/jitter-backoff';

@Injectable({ providedIn: 'root' })
export class JsonTranslationGateway implements TranslationGateway {
  async loadDictionary(locale: string): Promise<Record<string, string>> {
    return retryWithJitter(
      async () => {
        const res = await fetch(`/i18n/${locale}.json`);
        if (!res.ok) {
          throw new Error(`Failed to load locale ${locale}: HTTP ${res.status}`);
        }
        return (await res.json()) as Record<string, string>;
      },
      { baseDelayMs: 40, maxDelayMs: 400, maxAttempts: 2 },
    );
  }
}

@Pipe({
  name: 't',
  standalone: true,
  pure: false,
})
export class TranslatePipe implements PipeTransform {
  private readonly localeStore = inject(LocaleStore);

  transform(key: string): string {
    return this.localeStore.translate(key);
  }
}
