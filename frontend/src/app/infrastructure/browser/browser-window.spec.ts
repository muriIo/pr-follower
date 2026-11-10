import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import { BROWSER_WINDOW } from './browser-window';

describe('BROWSER_WINDOW', () => {
  it('defaults to the real window', () => {
    expect(TestBed.inject(BROWSER_WINDOW)).toBe(window);
  });
});
