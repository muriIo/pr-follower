import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { InspectionDrawerComponent } from './inspection-drawer.component';
import { loadWorkspaceFixture } from '../../../testing/workspace-fixtures';

describe('InspectionDrawerComponent', () => {
  it('renders docked PR inspection drawer with diff viewer and closes when close button is clicked', () => {
    TestBed.configureTestingModule({
      imports: [InspectionDrawerComponent],
      providers: [
        WorkspaceStore,
        {
          provide: PULL_REQUEST_GATEWAY,
          useValue: {
            fetchWorkspace: async () => null,
            approvePullRequest: async () => undefined,
            mergePullRequest: async () => ({ sha: 'a1b2c3d' }),
            commentOnPullRequest: async () => undefined,
            snoozePullRequest: async () => undefined,
            cancelServerOperation: async () => undefined,
          },
        },
        {
          provide: LIVE_EVENT_GATEWAY,
          useValue: {
            connect: (_onEvent: unknown, onState: (s: 'conectado') => void) => {
              onState('conectado');
              return () => undefined;
            },
          },
        },
        {
          provide: TRANSLATION_GATEWAY,
          useValue: { loadDictionary: async () => ({}) },
        },
      ],
    });

    const store = TestBed.inject(WorkspaceStore);

    loadWorkspaceFixture(store);
    store.openPr(142);

    const fixture = TestBed.createComponent(InspectionDrawerComponent);
    fixture.componentRef.setInput('drawer', store.drawer()!);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.pf-drawer-title')?.textContent).toContain('real-time CI status pulse');
    expect(el.querySelector('app-diff-viewer')).toBeTruthy();

    const closeBtn = el.querySelector('#pf-drawer-close') as HTMLButtonElement;
    closeBtn.click();
    expect(store.selectedPrId()).toBeNull();
  });

  it('resizes the docked drawer via pointer drag, double-click reset, and keyboard arrows', () => {
    TestBed.configureTestingModule({
      imports: [InspectionDrawerComponent],
      providers: [
        WorkspaceStore,
        {
          provide: PULL_REQUEST_GATEWAY,
          useValue: {
            fetchWorkspace: async () => null,
            approvePullRequest: async () => undefined,
            mergePullRequest: async () => ({ sha: 'a1b2c3d' }),
            commentOnPullRequest: async () => undefined,
            snoozePullRequest: async () => undefined,
            cancelServerOperation: async () => undefined,
          },
        },
        {
          provide: LIVE_EVENT_GATEWAY,
          useValue: {
            connect: (_onEvent: unknown, onState: (s: 'conectado') => void) => {
              onState('conectado');
              return () => undefined;
            },
          },
        },
        {
          provide: TRANSLATION_GATEWAY,
          useValue: { loadDictionary: async () => ({}) },
        },
      ],
    });

    const store = TestBed.inject(WorkspaceStore);

    loadWorkspaceFixture(store);
    store.resetDrawerWidth();
    store.openPr(142);

    const fixture = TestBed.createComponent(InspectionDrawerComponent);
    fixture.componentRef.setInput('drawer', store.drawer()!);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    const handle = el.querySelector('#pf-drawer-resize') as HTMLElement;
    expect(handle).toBeTruthy();

    // Pointer drag left by 120px (800 -> 680) widens right-docked drawer from 560px to 680px
    handle.dispatchEvent(new PointerEvent('pointerdown', { button: 0, clientX: 800, pointerId: 1, bubbles: true }));
    expect(store.isResizingDrawer()).toBe(true);

    handle.dispatchEvent(new PointerEvent('pointermove', { clientX: 680, pointerId: 1, bubbles: true }));
    fixture.detectChanges();
    expect(store.drawerWidth()).toBe(680);

    handle.dispatchEvent(new PointerEvent('pointerup', { clientX: 680, pointerId: 1, bubbles: true }));
    expect(store.isResizingDrawer()).toBe(false);

    // Double-click resets to 560px
    handle.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    expect(store.drawerWidth()).toBe(560);

    // Keyboard ArrowLeft (+24px), ArrowRight (-24px), Home (reset to 560px)
    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    expect(store.drawerWidth()).toBe(584);

    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
    expect(store.drawerWidth()).toBe(560);

    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }));
    expect(store.drawerWidth()).toBe(560);
  });
});
