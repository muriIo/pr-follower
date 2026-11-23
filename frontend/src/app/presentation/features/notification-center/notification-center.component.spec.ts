import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { NotificationStore } from '../../../application/stores/notification.store';
import { NotificationCenterComponent } from './notification-center.component';

function render() {
  TestBed.configureTestingModule({ imports: [NotificationCenterComponent] });
  const store = TestBed.inject(NotificationStore);
  store.isOpen.set(true);
  const fixture = TestBed.createComponent(NotificationCenterComponent);
  fixture.detectChanges();
  return { el: fixture.nativeElement as HTMLElement, store, fixture };
}

describe('NotificationCenterComponent', () => {
  it('shows the list first and switches to the settings', () => {
    const { el, store, fixture } = render();
    expect(el.querySelector('app-notification-list')).not.toBeNull();
    const tabs = el.querySelectorAll<HTMLButtonElement>('[role="tab"]');
    expect(tabs[0].getAttribute('aria-selected')).toBe('true');

    tabs[1].click();
    fixture.detectChanges();
    expect(store.tab()).toBe('settings');
    expect(el.querySelector('app-notification-settings')).not.toBeNull();
  });

  it('closes with its button and with Escape', () => {
    const { el, store } = render();
    (el.querySelector('.pf-icon-btn') as HTMLButtonElement).click();
    expect(store.isOpen()).toBe(false);
    store.isOpen.set(true);
    el.querySelector('aside')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(store.isOpen()).toBe(false);
  });
});
