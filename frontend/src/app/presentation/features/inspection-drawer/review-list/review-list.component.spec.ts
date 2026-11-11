import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { DrawerReviewViewModel } from '../../../../application/stores/workspace.store';
import { TRANSLATION_GATEWAY } from '../../../../domain/gateways/workspace.gateways';
import { ReviewListComponent } from './review-list.component';

const row = (reviewer: string, label: string, stale = ''): DrawerReviewViewModel => ({
  reviewer,
  label,
  badgeCls: 'pf-badge-baixo',
  stale,
});

function render(reviews: readonly DrawerReviewViewModel[]) {
  TestBed.configureTestingModule({
    imports: [ReviewListComponent],
    providers: [{ provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } }],
  });
  const fixture = TestBed.createComponent(ReviewListComponent);
  fixture.componentRef.setInput('reviews', reviews);
  fixture.detectChanges();
  return fixture.nativeElement as HTMLElement;
}

describe('ReviewListComponent', () => {
  it('lists each reviewer with what they decided', () => {
    const el = render([row('@bob', 'Aprovou'), row('@carol', 'Pediu alterações')]);
    const items = Array.from(el.querySelectorAll('li')).map((li) => li.textContent!);
    expect(items).toHaveLength(2);
    expect(items[0]).toContain('@bob');
    expect(items[0]).toContain('Aprovou');
    expect(items[1]).toContain('@carol');
    expect(items[1]).toContain('Pediu alterações');
  });

  it('says when a review was of an older commit', () => {
    const el = render([row('@bob', 'Aprovou', 'em um commit anterior')]);
    expect(el.textContent).toContain('em um commit anterior');
    expect(el.querySelector('li')!.classList.contains('is-stale')).toBe(true);
  });

  it('renders nothing for a PR nobody reviewed', () => {
    expect(render([]).querySelector('li')).toBeNull();
  });
});
