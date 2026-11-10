import { describe, expect, it } from 'vitest';
import {
  createInitialPrDataset,
  createInitialRepos,
  createInitialTimelineEvents,
  loadWorkspaceFixture,
} from './workspace-fixtures';

describe('workspace fixtures (test-only data)', () => {
  it('is internally consistent: unique PR ids, and events refer to known repositories', () => {
    const prs = createInitialPrDataset();
    expect(prs.length).toBeGreaterThan(0);
    expect(new Set(prs.map((p) => p.id)).size).toBe(prs.length);
    // Some PRs come from repositories the user does not follow (CODEOWNERS inbox cases).
    const known = new Set([...createInitialRepos().map((r) => r.name), ...prs.map((p) => p.repo)]);
    for (const ev of createInitialTimelineEvents(0)) {
      expect(known.has(ev.repo)).toBe(true);
    }
  });

  it('loads into a store-shaped object', () => {
    const set = <T,>() => {
      const box: { value: T | null; set(v: T): void } = { value: null, set: (v) => (box.value = v) };
      return box;
    };
    const store = { repos: set<never>(), prs: set<never>(), events: set<never>() };
    loadWorkspaceFixture(store as never);
    expect((store.repos.value as unknown as unknown[]).length).toBe(createInitialRepos().length);
    expect((store.prs.value as unknown as unknown[]).length).toBe(createInitialPrDataset().length);
  });
});
