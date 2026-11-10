import { describe, expect, it } from 'vitest';
import { ImportableRepository, filterImportableRepositories } from './repository-import.models';

function repo(fullName: string, followed = false): ImportableRepository {
  const [owner, name] = fullName.split('/');
  return {
    githubRepoId: fullName.length,
    installationId: 1,
    owner,
    name,
    fullName,
    defaultBranch: 'main',
    private: false,
    followed,
    repositoryId: followed ? 9 : null,
  };
}

describe('filterImportableRepositories', () => {
  const repos = [repo('acme/web'), repo('acme/API-gateway'), repo('acme/docs', true)];

  it('sorts alphabetically, case-insensitively', () => {
    expect(filterImportableRepositories(repos, '').map((r) => r.fullName)).toEqual([
      'acme/API-gateway',
      'acme/docs',
      'acme/web',
    ]);
  });

  it('matches the query anywhere in the full name, ignoring case and surrounding spaces', () => {
    expect(filterImportableRepositories(repos, '  api ').map((r) => r.fullName)).toEqual(['acme/API-gateway']);
    expect(filterImportableRepositories(repos, 'ACME/').length).toBe(3);
    expect(filterImportableRepositories(repos, 'nothing')).toEqual([]);
  });
});
