import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { PageHeaderComponent } from './page-header.component';

describe('PageHeaderComponent', () => {
  it('renders perspective title and optional subtitle', () => {
    TestBed.configureTestingModule({ imports: [PageHeaderComponent] });
    const fixture = TestBed.createComponent(PageHeaderComponent);
    fixture.componentRef.setInput('title', 'Seats de Repositórios');
    fixture.componentRef.setInput('subtitle', 'Visão ao vivo dos repositórios seguidos pelo time.');
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('h1.pf-title')?.textContent).toBe('Seats de Repositórios');
    expect(el.querySelector('p.pf-sub')?.textContent).toBe(
      'Visão ao vivo dos repositórios seguidos pelo time.',
    );
  });
});
