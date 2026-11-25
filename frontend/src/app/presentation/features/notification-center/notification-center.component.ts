import { ChangeDetectionStrategy, Component, ElementRef, HostListener, inject } from '@angular/core';
import { NotificationStore } from '../../../application/stores/notification.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { NotificationListComponent } from './notification-list/notification-list.component';
import { NotificationSettingsComponent } from './notification-settings/notification-settings.component';

/** The notification center: a side panel over the workspace with the list and the settings. */
@Component({
  selector: 'app-notification-center',
  standalone: true,
  imports: [NotificationListComponent, NotificationSettingsComponent, TranslatePipe],
  templateUrl: './notification-center.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class NotificationCenterComponent {
  readonly store = inject(NotificationStore);
  private readonly hostRef = inject(ElementRef<HTMLElement>);

  private dragStartX = 0;
  private dragStartWidth = 0;

  /** Dragging the left edge resizes the panel, as it does the PR drawer. */
  onResizeStart(event: PointerEvent): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    this.dragStartX = event.clientX;
    this.dragStartWidth = this.store.width();
    this.store.startResize();
    (event.currentTarget as HTMLElement | null)?.setPointerCapture?.(event.pointerId);
  }

  @HostListener('window:pointermove', ['$event'])
  onResizeMove(event: PointerEvent): void {
    if (!this.store.isResizing()) {
      return;
    }
    this.store.setWidth(this.dragStartWidth + this.dragStartX - event.clientX, this.maxWidth());
  }

  @HostListener('window:pointerup', ['$event'])
  @HostListener('window:pointercancel', ['$event'])
  onResizeEnd(event?: PointerEvent): void {
    if (!this.store.isResizing()) {
      return;
    }
    const target = event?.currentTarget as HTMLElement | null | undefined;
    if (event && target?.hasPointerCapture?.(event.pointerId)) {
      target.releasePointerCapture(event.pointerId);
    }
    this.store.endResize();
  }

  onResizeKeydown(event: KeyboardEvent): void {
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      this.store.stepWidth(24, this.maxWidth());
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      this.store.stepWidth(-24, this.maxWidth());
    } else if (event.key === 'Home') {
      event.preventDefault();
      this.store.resetWidth();
    }
  }

  /** Leaves the sidebar and some of the workspace visible. */
  private maxWidth(): number {
    const appEl = (this.hostRef.nativeElement as HTMLElement).closest('.pf-app') as HTMLElement | null;
    if (!appEl || appEl.clientWidth <= 0) {
      return 960;
    }
    const sideWidth = appEl.querySelector<HTMLElement>('.pf-side')?.offsetWidth ?? 216;
    return Math.max(320, appEl.clientWidth - sideWidth - 360);
  }
}
