import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { ActivityIconName, ActivityTone } from '../../../domain/models/activity.models';

/** 16px stroke icons for timeline activity (merge, approval, CI, access...). */
const PATHS: Readonly<Record<Exclude<ActivityIconName, 'dot'>, string>> = {
  merge: 'M5.5 3.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M5.5 12.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M13.5 8.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M4 5v6M4 5c0 2.6 2.4 3.5 6.5 3.5',
  approve: 'M3 8.5l3 3 7-7',
  'pr-open': 'M5.5 3.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M5.5 12.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M13.5 12.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M4 5v6M12 11V6a2 2 0 0 0-2-2H7M8.5 2.5L7 4l1.5 1.5',
  'pr-closed': 'M5.5 3.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M5.5 12.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M13.5 12.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M4 5v6M12 8v3M10 2.5l4 4M14 2.5l-4 4',
  draft: 'M5.5 3.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M5.5 12.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M13.5 12.5a1.5 1.5 0 1 1-3 0a1.5 1.5 0 1 1 3 0M4 5v6M12 4.5v.01M12 7.5v.01',
  commit: 'M10.5 8a2.5 2.5 0 1 1-5 0a2.5 2.5 0 1 1 5 0M1.5 8h4M10.5 8h4',
  edit: 'M11 2.5l2.5 2.5L6 12.5H3.5V10z',
  claim: 'M10.5 5a2.5 2.5 0 1 1-5 0a2.5 2.5 0 1 1 5 0M3 14c0-2.8 2.2-4.5 5-4.5s5 1.7 5 4.5',
  snooze: 'M4 11h8l-1-2V6.5a3 3 0 0 0-6 0V9zM6.5 13a1.5 1.5 0 0 0 3 0M2 2l12 12',
  'ci-pass': 'M14 8a6 6 0 1 1-12 0a6 6 0 1 1 12 0M5.5 8.2l1.8 1.8 3.4-3.6',
  'ci-fail': 'M14 8a6 6 0 1 1-12 0a6 6 0 1 1 12 0M6 6l4 4M10 6l-4 4',
  'ci-run': 'M14 8a6 6 0 1 1-12 0a6 6 0 1 1 12 0M8 5v3l2 1.5',
  comment: 'M2.5 3.5h11v7H8l-3 2.5v-2.5H2.5z',
  shield: 'M8 1.8l5 2v4c0 3-2.2 5.2-5 6.4C5.2 13 3 10.8 3 7.8v-4z',
  'repo-add': 'M2.5 2.5h7v11h-7zM12.5 4v5M10 6.5h5',
  lock: 'M4 7h8v6.5H4zM5.5 7V5a2.5 2.5 0 0 1 5 0v2',
  unlock: 'M4 7h8v6.5H4zM5.5 7V5a2.5 2.5 0 0 1 4.9-.7',
};

@Component({
  selector: 'app-activity-icon',
  standalone: true,
  templateUrl: './activity-icon.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ActivityIconComponent {
  readonly icon = input.required<ActivityIconName>();
  readonly tone = input.required<ActivityTone>();
  readonly path = computed(() => {
    const icon = this.icon();
    return icon === 'dot' ? '' : PATHS[icon];
  });
}
