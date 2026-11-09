import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { DiffViewerComponent } from './diff-viewer.component';

describe('DiffViewerComponent', () => {
  it('renders file header, delta counts, and syntax-tokenized diff lines', () => {
    TestBed.configureTestingModule({ imports: [DiffViewerComponent] });
    const fixture = TestBed.createComponent(DiffViewerComponent);
    fixture.componentRef.setInput('file', 'src/app/seat-card.component.ts');
    fixture.componentRef.setInput('deltaAdd', '+24');
    fixture.componentRef.setInput('deltaDel', '-3');
    fixture.componentRef.setInput('lines', [
      {
        cls: 'pf-line-add',
        n: '18',
        tokens: [
          { s: '+ ', cls: 'pf-tk-add' },
          { s: 'readonly', cls: 'pf-tk-kw' },
          { s: ' pulsing = true;', cls: '' },
        ],
      },
    ]);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('src/app/seat-card.component.ts');
    expect(el.textContent).toContain('+24');
    expect(el.textContent).toContain('-3');
    expect(el.querySelector('.pf-line-add')).toBeTruthy();
    expect(el.querySelector('.pf-tk-kw')?.textContent).toBe('readonly');
  });
});
