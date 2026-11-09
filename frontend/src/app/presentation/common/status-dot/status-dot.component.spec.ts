import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { StatusDotComponent } from './status-dot.component';

describe('StatusDotComponent', () => {
  it('renders status dot with computed classes', () => {
    TestBed.configureTestingModule({ imports: [StatusDotComponent] });
    const fixture = TestBed.createComponent(StatusDotComponent);
    fixture.componentRef.setInput('dotClass', 'pf-dot pf-dot-ok pf-dot-pulse');
    fixture.detectChanges();

    const dot = fixture.nativeElement.querySelector('span.pf-dot') as HTMLElement;
    expect(dot).toBeTruthy();
    expect(dot.className).toContain('pf-dot-ok');
    expect(dot.className).toContain('pf-dot-pulse');
  });
});
