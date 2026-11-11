import { describe, expect, it } from 'vitest';
import { activityIconFor } from './activity.models';

describe('activityIconFor', () => {
  it('gives each kind of activity its own icon and tone', () => {
    expect(activityIconFor('pr.merged', 'ok')).toEqual({ icon: 'merge', tone: 'merged' });
    expect(activityIconFor('pr.approved', 'ok')).toEqual({ icon: 'approve', tone: 'ok' });
    expect(activityIconFor('pr.opened', 'info')).toEqual({ icon: 'pr-open', tone: 'open' });
    expect(activityIconFor('repository.codeowners_updated', 'info')).toEqual({ icon: 'edit', tone: 'info' });
    expect(activityIconFor('pr.changes_requested', 'info')).toEqual({ icon: 'comment', tone: 'warn' });
    expect(activityIconFor('pr.reviewed', 'info')).toEqual({ icon: 'comment', tone: 'info' });
    expect(activityIconFor('pr.review_dismissed', 'info')).toEqual({ icon: 'dot', tone: 'info' });
    expect(activityIconFor('pr.reopened', 'info')).toEqual({ icon: 'pr-open', tone: 'open' });
    expect(activityIconFor('pr.closed', 'info')).toEqual({ icon: 'pr-closed', tone: 'fail' });
    expect(activityIconFor('pr.synchronize', 'info')).toEqual({ icon: 'commit', tone: 'info' });
    expect(activityIconFor('pr.edited', 'info')).toEqual({ icon: 'edit', tone: 'info' });
    expect(activityIconFor('pr.claimed', 'turn')).toEqual({ icon: 'claim', tone: 'turn' });
    expect(activityIconFor('pr.snoozed', 'info')).toEqual({ icon: 'snooze', tone: 'info' });
    expect(activityIconFor('ci.passed', 'ok')).toEqual({ icon: 'ci-pass', tone: 'ok' });
    expect(activityIconFor('ci.failed', 'fail')).toEqual({ icon: 'ci-fail', tone: 'fail' });
    expect(activityIconFor('ci.running', 'info')).toEqual({ icon: 'ci-run', tone: 'warn' });
    expect(activityIconFor('dependabot.command', 'info')).toEqual({ icon: 'comment', tone: 'info' });
    expect(activityIconFor('dependabot.analyzed', 'info')).toEqual({ icon: 'shield', tone: 'info' });
    expect(activityIconFor('repository.followed', 'info')).toEqual({ icon: 'repo-add', tone: 'info' });
    expect(activityIconFor('repository.access_lost', 'fail')).toEqual({ icon: 'lock', tone: 'fail' });
    expect(activityIconFor('repository.access_suspended', 'fail')).toEqual({ icon: 'lock', tone: 'fail' });
    expect(activityIconFor('repository.access_restored', 'ok')).toEqual({ icon: 'unlock', tone: 'ok' });
  });

  it('falls back to a dot coloured by kind for unknown activity', () => {
    expect(activityIconFor(undefined, 'fail')).toEqual({ icon: 'dot', tone: 'fail' });
    expect(activityIconFor('something.new', 'info')).toEqual({ icon: 'dot', tone: 'info' });
  });
});
