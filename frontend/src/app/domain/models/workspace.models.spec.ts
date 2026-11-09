import { describe, expect, it } from 'vitest';
import {
  computeSemverBumpLabel,
  createInitialPrDataset,
  isActionableTurn,
  isSafeBatchCandidate,
} from './workspace.models';

describe('Domain Models & Policies (workspace.models)', () => {
  it('computes Major, Minor, and Patch semver bump labels accurately', () => {
    expect(computeSemverBumpLabel('1.7.7', '2.0.0')).toBe('Major');
    expect(computeSemverBumpLabel('4.28.1', '4.29.0')).toBe('Minor');
    expect(computeSemverBumpLabel('3.3.6', '3.3.8')).toBe('Patch');
  });

  it('determines actionable CODEOWNERS turn state respecting silenced and approved maps', () => {
    const prs = createInitialPrDataset();
    const pr142 = prs.find((p) => p.id === 142)!;
    const pr139 = prs.find((p) => p.id === 139)!;

    expect(isActionableTurn(pr142, {}, {})).toBe(true);
    expect(isActionableTurn(pr142, { 142: '🔕 Silenciado' }, {})).toBe(false);
    expect(isActionableTurn(pr142, {}, { 142: true })).toBe(false);
    expect(isActionableTurn(pr139, {}, {})).toBe(false);
  });

  it('identifies low-risk passing Dependabot PRs as safe batch candidates', () => {
    const prs = createInitialPrDataset();
    const vitestPr = prs.find((p) => p.id === 145)!;
    const axiosPr = prs.find((p) => p.id === 89)!;
    const pgFailingPr = prs.find((p) => p.id === 231)!;

    expect(isSafeBatchCandidate(vitestPr)).toBe(true);
    expect(isSafeBatchCandidate(vitestPr, 'fail')).toBe(false);
    expect(isSafeBatchCandidate(axiosPr)).toBe(false);
    expect(isSafeBatchCandidate(pgFailingPr)).toBe(false);
  });
});
