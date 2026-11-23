import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { LocaleStore } from '../../../application/stores/locale.store';
import { NotificationStore } from '../../../application/stores/notification.store';
import { NotificationBellComponent } from './notification-bell.component';

function render(unread: number) {
  TestBed.configureTestingModule({ imports: [NotificationBellComponent] });
  const store = TestBed.inject(NotificationStore);
  store.unread.set(unread);
  const fixture = TestBed.createComponent(NotificationBellComponent);
  fixture.detectChanges();
  return { el: fixture.nativeElement as HTMLElement, store, fixture, t: TestBed.inject(LocaleStore) };
}

describe('NotificationBellComponent', () => {
  it('counts the unread notifications, 9+ above nine, and says so to assistive tech', () => {
    const { el, t } = render(12);
    const button = el.querySelector('button')!;
    expect(el.querySelector('.pf-bell-badge')?.textContent?.trim()).toBe('9+');
    expect(button.getAttribute('aria-label')).toBe(t.translate('notifications.bell.unread', { count: 12 }));
  });

  it('shows no badge when everything is read', () => {
    const { el } = render(0);
    expect(el.querySelector('.pf-bell-badge')).toBeNull();
  });

  it('opens and closes the center', () => {
    const { el, store, fixture } = render(1);
    const button = el.querySelector('button')!;
    button.click();
    fixture.detectChanges();
    expect(store.isOpen()).toBe(true);
    expect(button.getAttribute('aria-expanded')).toBe('true');
    button.click();
    expect(store.isOpen()).toBe(false);
  });
});
