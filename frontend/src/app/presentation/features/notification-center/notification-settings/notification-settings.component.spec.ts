import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { LocaleStore } from '../../../../application/stores/locale.store';
import { NotificationStatus, NotificationStore } from '../../../../application/stores/notification.store';
import { BrowserPermission } from '../../../../domain/models/notification.models';
import { NotificationSettingsComponent } from './notification-settings.component';

function render(permission: BrowserPermission, paused = false) {
  TestBed.configureTestingModule({ imports: [NotificationSettingsComponent] });
  const store = TestBed.inject(NotificationStore);
  store.permission.set(permission);
  store.options.set({ settings: { paused, popupsOff: ['risk'] }, channels: ['panel'], kinds: ['turn', 'risk', 'involved'] });
  const fixture = TestBed.createComponent(NotificationSettingsComponent);
  fixture.detectChanges();
  return { el: fixture.nativeElement as HTMLElement, store, t: TestBed.inject(LocaleStore) };
}

describe('NotificationSettingsComponent', () => {
  const cases: [BrowserPermission, boolean, NotificationStatus][] = [
    ['unsupported', false, 'unsupported'],
    ['default', false, 'default'],
    ['granted', false, 'active'],
    ['granted', true, 'paused'],
    ['denied', false, 'denied'],
  ];
  for (const [permission, paused, status] of cases) {
    it(`shows the ${status} state with its explanation`, () => {
      const { el, t } = render(permission, paused);
      expect(el.querySelector('[role="status"]')?.textContent).toContain(t.translate(`notifications.status.${status}`));
      expect(el.textContent).toContain(t.translate(`notifications.status.${status}.help`));
      expect(!!el.querySelector('.pf-btn-primary')).toBe(status === 'default');
    });
  }

  it('asks the browser only from the button', () => {
    const { el, store } = render('default');
    const enable = vi.spyOn(store, 'enable').mockResolvedValue();
    (el.querySelector('.pf-btn-primary') as HTMLButtonElement).click();
    expect(enable).toHaveBeenCalled();
  });

  it('pauses, and switches the popups of each kind', () => {
    const { el, store } = render('granted');
    const setPaused = vi.spyOn(store, 'setPaused').mockResolvedValue();
    const toggle = vi.spyOn(store, 'togglePopups').mockResolvedValue();
    const switches = [...el.querySelectorAll<HTMLButtonElement>('[role="switch"]')];
    expect(switches.map((s) => s.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false', 'true']);
    switches[0].click();
    expect(setPaused).toHaveBeenCalledWith(true);
    switches[2].click();
    expect(toggle).toHaveBeenCalledWith('risk');
  });

  it('says when this server does not deliver with the panel closed', () => {
    const { el, t } = render('granted');
    expect(el.textContent).toContain(t.translate('notifications.push.unavailable'));
  });
});
