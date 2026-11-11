import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { TRANSLATION_GATEWAY } from '../../../domain/gateways/workspace.gateways';
import { NoticeBarComponent } from './notice-bar.component';

function render(text: string) {
  TestBed.configureTestingModule({
    imports: [NoticeBarComponent],
    providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
  });
  const fixture = TestBed.createComponent(NoticeBarComponent);
  fixture.componentRef.setInput('text', text);
  const dismissed = vi.fn();
  fixture.componentInstance.dismissed.subscribe(dismissed);
  fixture.detectChanges();
  return { el: fixture.nativeElement as HTMLElement, dismissed };
}

describe('NoticeBarComponent', () => {
  it('announces what changed and lets the user dismiss it', () => {
    const { el, dismissed } = render('O PR #11 foi mesclado.');

    const bar = el.querySelector('[role="status"]')!;
    expect(bar.textContent).toContain('O PR #11 foi mesclado.');

    (el.querySelector('button') as HTMLButtonElement).click();
    expect(dismissed).toHaveBeenCalledTimes(1);
  });
});
