import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { ActivityIconComponent } from './activity-icon.component';

describe('ActivityIconComponent', () => {
  it('draws the requested icon in its tone', () => {
    TestBed.configureTestingModule({ imports: [ActivityIconComponent] });
    const fixture = TestBed.createComponent(ActivityIconComponent);
    fixture.componentRef.setInput('icon', 'merge');
    fixture.componentRef.setInput('tone', 'merged');
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const badge = el.querySelector('.pf-act') as HTMLElement;
    expect(badge.classList).toContain('pf-act-merged');
    expect(badge.getAttribute('data-icon')).toBe('merge');
    expect(badge.querySelector('svg path')).toBeTruthy();

    fixture.componentRef.setInput('icon', 'dot');
    fixture.componentRef.setInput('tone', 'info');
    fixture.detectChanges();
    expect(el.querySelector('.pf-act-dot')).toBeTruthy();
    expect(el.querySelector('svg')).toBeNull();
  });
});
