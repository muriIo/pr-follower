import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionExpiryMonitor } from '../../application/use-cases/session-expiry.monitor';
import { ActionError } from '../../domain/models/action-error.models';
import { HttpNotificationGateway } from './http-notification.gateway';

const NOTIFICATION_DTO = {
  id: 41,
  kind: 'turn',
  reason: 'new_commit',
  repository_id: 10,
  repository_name: 'arvore/hive-web',
  pull_request_id: 77,
  pr_number: 35,
  pr_title: 'Fix login',
  commit_sha: 's2',
  actor_login: 'carol',
  details: { title: 'Fix login' },
  created_at: '2026-11-23T09:00:00Z',
  read_at: '2026-11-23T10:00:00Z',
  pop_up: true,
};

const OPTIONS_DTO = {
  settings: { paused: true, popups_off: ['risk'] },
  channels: [{ name: 'panel' }],
  kinds: ['turn', 'risk', 'involved'],
};

function gateway(): HttpNotificationGateway {
  TestBed.configureTestingModule({ providers: [HttpNotificationGateway] });
  return TestBed.inject(HttpNotificationGateway);
}

describe('HttpNotificationGateway', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    TestBed.resetTestingModule();
  });

  it('lists a page and maps every field', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ notifications: [NOTIFICATION_DTO], unread: 3, next_before: 41 }), { status: 200 }),
    );
    const page = await gateway().list(50);

    expect(String(fetchSpy.mock.calls[0][0])).toBe('/api/v1/notifications?before=50');
    expect(page).toEqual({
      unread: 3,
      nextBefore: 41,
      items: [
        {
          id: 41,
          kind: 'turn',
          reason: 'new_commit',
          repositoryId: 10,
          repositoryName: 'arvore/hive-web',
          pullRequestId: 77,
          prNumber: 35,
          prTitle: 'Fix login',
          actorLogin: 'carol',
          details: { title: 'Fix login' },
          createdAt: Date.parse('2026-11-23T09:00:00Z'),
          read: true,
          popUp: true,
        },
      ],
    });
  });

  it('asks for the newest page without a cursor and ends paging without one', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ notifications: [], unread: 0 }), { status: 200 }),
    );
    const page = await gateway().list(null);
    expect(String(fetchSpy.mock.calls[0][0])).toBe('/api/v1/notifications');
    expect(page.nextBefore).toBeNull();
  });

  it('marks read by ids or all', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response('{"ok":true}', { status: 200 }));
    const gw = gateway();
    await gw.markRead([1, 2]);
    await gw.markRead('all');
    expect(fetchSpy.mock.calls.map((c) => [String(c[0]), (c[1] as RequestInit).method, (c[1] as RequestInit).body])).toEqual([
      ['/api/v1/notifications/read', 'POST', '{"ids":[1,2]}'],
      ['/api/v1/notifications/read', 'POST', '{"all":true}'],
    ]);
  });

  it('reads and saves the settings with what the server offers', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify(OPTIONS_DTO), { status: 200 }));
    const gw = gateway();
    const expected = { settings: { paused: true, popupsOff: ['risk'] }, channels: ['panel'], kinds: ['turn', 'risk', 'involved'] };

    expect(await gw.options()).toEqual(expected);
    expect(await gw.saveSettings({ paused: true, popupsOff: ['risk'] })).toEqual(expected);
    const [url, init] = fetchSpy.mock.calls[1];
    expect(String(url)).toBe('/api/v1/notifications/settings');
    expect((init as RequestInit).method).toBe('PUT');
    expect((init as RequestInit).body).toBe('{"paused":true,"popups_off":["risk"]}');
  });

  it('rejects with an ActionError carrying the server code, and reports an expired session', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'bad', code: 'invalid_input' }), { status: 400 }),
    );
    await expect(gateway().markRead([])).rejects.toEqual(new ActionError('invalid_input', 'bad'));

    TestBed.resetTestingModule();
    vi.restoreAllMocks();
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ code: 'unauthorized' }), { status: 401 }));
    const gw = gateway();
    const report = vi.spyOn(TestBed.inject(SessionExpiryMonitor), 'report').mockImplementation(() => undefined);
    await expect(gw.options()).rejects.toBeInstanceOf(ActionError);
    expect(report).toHaveBeenCalledWith('unauthorized');
  });

  it('turns a network failure into an ActionError', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('offline'));
    await expect(gateway().list(null)).rejects.toEqual(new ActionError('unknown'));
  });
});
