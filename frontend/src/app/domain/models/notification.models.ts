/** What a notification is about (GLOSSARY.md); each kind has its own look and on/off switch. */
export type NotificationKind = 'turn' | 'risk' | 'involved';

export const NOTIFICATION_KINDS: readonly NotificationKind[] = ['turn', 'risk', 'involved'];

/** Why it was created; the wording follows it. */
export type NotificationReason =
  | 'review_requested'
  | 'new_commit'
  | 'snooze_ended'
  | 'author_replied'
  | 'high_risk'
  | 'merged'
  | 'closed'
  | 'approval_dismissed'
  | 'ci_failed';

/** One notification as the API keeps it: facts, never text (ADR-0017). */
export interface NotificationItem {
  readonly id: number;
  readonly kind: NotificationKind;
  readonly reason: NotificationReason;
  readonly repositoryId: number;
  readonly repositoryName: string;
  readonly pullRequestId: number;
  readonly prNumber: number;
  readonly prTitle: string;
  readonly actorLogin: string;
  readonly details: Readonly<Record<string, string>>;
  /** Epoch milliseconds. */
  readonly createdAt: number;
  readonly read: boolean;
  /** Whether the owner's settings let it pop up (only on live delivery). */
  readonly popUp: boolean;
}

export interface NotificationPage {
  readonly items: readonly NotificationItem[];
  readonly unread: number;
  /** Cursor of the next (older) page; null on the last one. */
  readonly nextBefore: number | null;
}

/** The user's choices, stored on the server so they hold on every device. */
export interface NotificationSettings {
  /** Stops every popup; the center still lists everything. */
  readonly paused: boolean;
  /** Kinds that never pop up. */
  readonly popupsOff: readonly NotificationKind[];
}

export interface NotificationOptions {
  readonly settings: NotificationSettings;
  /** Delivery channels this server offers (`panel`, later `webpush`). */
  readonly channels: readonly string[];
  readonly kinds: readonly NotificationKind[];
}

/**
 * The browser's permission to show system notifications for this site, plus `unsupported`
 * (no Notification API, or the page is not a secure context).
 */
export type BrowserPermission = 'unsupported' | 'default' | 'granted' | 'denied';

/** What a system popup shows; built from the user's dictionary. */
export interface SystemPopup {
  readonly title: string;
  readonly body: string;
  /** Popups with the same tag replace each other instead of stacking. */
  readonly tag: string;
}
