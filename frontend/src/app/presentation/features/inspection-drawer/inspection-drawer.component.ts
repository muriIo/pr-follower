import { ChangeDetectionStrategy, Component, ElementRef, HostListener, inject, input } from '@angular/core';
import { DrawerViewModel, WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { CancellableActionComponent } from '../../common/cancellable-action/cancellable-action.component';
import { ChangedFilesComponent } from './changed-files/changed-files.component';
import { RiskBadgeComponent } from '../../common/risk-badge/risk-badge.component';
import { StatusDotComponent } from '../../common/status-dot/status-dot.component';
import { DependabotImpactComponent } from './dependabot-impact/dependabot-impact.component';
import { ReviewListComponent } from './review-list/review-list.component';

@Component({
  selector: 'app-inspection-drawer',
  standalone: true,
  imports: [
    StatusDotComponent,
    RiskBadgeComponent,
    CancellableActionComponent,
    ChangedFilesComponent,
    DependabotImpactComponent,
    ReviewListComponent,
    TranslatePipe,
  ],
  templateUrl: './inspection-drawer.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class InspectionDrawerComponent {
  readonly store = inject(WorkspaceStore);
  private readonly hostRef = inject(ElementRef<HTMLElement>);
  readonly drawer = input.required<DrawerViewModel>();

  private dragStartX = 0;
  private dragStartWidth = 560;

  onResizeStart(event: PointerEvent): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    this.dragStartX = event.clientX;
    this.dragStartWidth = this.store.drawerWidth();
    this.store.startDrawerResize();
    const target = event.currentTarget as HTMLElement | null;
    target?.setPointerCapture?.(event.pointerId);
  }

  @HostListener('window:pointermove', ['$event'])
  onResizeMove(event: PointerEvent): void {
    if (!this.store.isResizingDrawer()) {
      return;
    }
    const delta = this.dragStartX - event.clientX;
    this.store.setDrawerWidth(this.dragStartWidth + delta, this.computeMaxDrawerWidth());
  }

  @HostListener('window:pointerup', ['$event'])
  @HostListener('window:pointercancel', ['$event'])
  onResizeEnd(event?: PointerEvent): void {
    if (!this.store.isResizingDrawer()) {
      return;
    }
    if (event) {
      const target = event.currentTarget as HTMLElement | null;
      if (target?.hasPointerCapture?.(event.pointerId)) {
        target.releasePointerCapture(event.pointerId);
      }
    }
    this.store.endDrawerResize();
  }

  onResizeKeydown(event: KeyboardEvent): void {
    const maxPx = this.computeMaxDrawerWidth();
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      this.store.stepDrawerWidth(24, maxPx);
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      this.store.stepDrawerWidth(-24, maxPx);
    } else if (event.key === 'Home') {
      event.preventDefault();
      this.store.resetDrawerWidth();
    }
  }

  private computeMaxDrawerWidth(): number {
    const appEl = this.hostRef.nativeElement.closest('.pf-app') as HTMLElement | null;
    if (!appEl || appEl.clientWidth <= 0) {
      return 960;
    }
    const sideWidth = appEl.querySelector<HTMLElement>('.pf-side')?.offsetWidth ?? 216;
    return Math.max(420, appEl.clientWidth - sideWidth - 360);
  }
}
