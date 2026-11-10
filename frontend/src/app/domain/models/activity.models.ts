import { TimelineEventItem } from './workspace.models';

export type ActivityIconName =
  | 'merge'
  | 'approve'
  | 'pr-open'
  | 'pr-closed'
  | 'draft'
  | 'commit'
  | 'edit'
  | 'claim'
  | 'snooze'
  | 'ci-pass'
  | 'ci-fail'
  | 'ci-run'
  | 'comment'
  | 'shield'
  | 'repo-add'
  | 'lock'
  | 'unlock'
  | 'dot';

export type ActivityTone = 'ok' | 'fail' | 'warn' | 'turn' | 'info' | 'merged' | 'open';

export interface ActivityIcon {
  readonly icon: ActivityIconName;
  readonly tone: ActivityTone;
}

const BY_TYPE: Readonly<Record<string, ActivityIcon>> = {
  'pr.merged': { icon: 'merge', tone: 'merged' },
  'pr.approved': { icon: 'approve', tone: 'ok' },
  'pr.opened': { icon: 'pr-open', tone: 'open' },
  'pr.reopened': { icon: 'pr-open', tone: 'open' },
  'pr.ready_for_review': { icon: 'pr-open', tone: 'open' },
  'pr.converted_to_draft': { icon: 'draft', tone: 'info' },
  'pr.closed': { icon: 'pr-closed', tone: 'fail' },
  'pr.synchronize': { icon: 'commit', tone: 'info' },
  'pr.edited': { icon: 'edit', tone: 'info' },
  'pr.claimed': { icon: 'claim', tone: 'turn' },
  'pr.snoozed': { icon: 'snooze', tone: 'info' },
  'ci.passed': { icon: 'ci-pass', tone: 'ok' },
  'ci.failed': { icon: 'ci-fail', tone: 'fail' },
  'ci.running': { icon: 'ci-run', tone: 'warn' },
  'dependabot.command': { icon: 'comment', tone: 'info' },
  'dependabot.analyzed': { icon: 'shield', tone: 'info' },
  'repository.followed': { icon: 'repo-add', tone: 'info' },
  'repository.access_lost': { icon: 'lock', tone: 'fail' },
  'repository.access_suspended': { icon: 'lock', tone: 'fail' },
  'repository.access_restored': { icon: 'unlock', tone: 'ok' },
};

/** The icon for an activity type; unknown activity falls back to a dot coloured by its kind. */
export function activityIconFor(type: string | undefined, kind: TimelineEventItem['kind']): ActivityIcon {
  return (type && BY_TYPE[type]) || { icon: 'dot', tone: kind };
}
