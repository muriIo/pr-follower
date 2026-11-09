import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { RiskBadgeComponent } from './risk-badge.component';

describe('RiskBadgeComponent', () => {
  it('renders badge with label, variant class, and optional tooltip', () => {
    TestBed.configureTestingModule({ imports: [RiskBadgeComponent] });
    const fixture = TestBed.createComponent(RiskBadgeComponent);
    fixture.componentRef.setInput('label', '🟢 Baixo Risco');
    fixture.componentRef.setInput('badgeClass', 'pf-badge-baixo');
    fixture.componentRef.setInput('tooltip', 'Seguro para merge rápido');
    fixture.detectChanges();

    const badge = fixture.nativeElement.querySelector('span.pf-badge') as HTMLElement;
    expect(badge).toBeTruthy();
    expect(badge.textContent).toContain('🟢 Baixo Risco');
    expect(badge.className).toContain('pf-badge-baixo');
    expect(badge.getAttribute('data-tip')).toBe('Seguro para merge rápido');
  });
});
