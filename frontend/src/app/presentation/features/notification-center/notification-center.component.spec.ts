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

  it('resizes like the other drawers: drag, double-click reset and arrow keys', () => {
    const { el, store, fixture } = render();
    store.resetWidth();
    fixture.detectChanges();
    const handle = el.querySelector('#pf-notif-resize') as HTMLElement;
    expect(handle).toBeTruthy();
    expect(el.querySelector('aside')!.style.getPropertyValue('--pf-nw')).toBe('400px');

    handle.dispatchEvent(new PointerEvent('pointerdown', { button: 0, clientX: 800, pointerId: 1, bubbles: true }));
    expect(store.isResizing()).toBe(true);
    handle.dispatchEvent(new PointerEvent('pointermove', { clientX: 700, pointerId: 1, bubbles: true }));
    fixture.detectChanges();
    expect(store.width()).toBe(500);
    expect(el.querySelector('aside')!.style.getPropertyValue('--pf-nw')).toBe('500px');
    handle.dispatchEvent(new PointerEvent('pointerup', { clientX: 700, pointerId: 1, bubbles: true }));
    expect(store.isResizing()).toBe(false);

    handle.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    expect(store.width()).toBe(400);
    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    expect(store.width()).toBe(424);
    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
    expect(store.width()).toBe(400);
    store.resetWidth();
  });
});
