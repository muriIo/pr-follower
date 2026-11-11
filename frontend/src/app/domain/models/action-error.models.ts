/** Why an action on GitHub did not happen, as the server reports it (stable codes). */
export type ActionErrorCode =
  | 'own_pull_request'
  | 'outcome_unknown'
  | 'forbidden'
  | 'conflict'
  | 'not_found'
  | 'unauthorized'
  | 'invalid_input'
  | 'unavailable'
  | 'not_analysable'
  | 'internal'
  | 'unknown';

const KNOWN_CODES: readonly string[] = [
  'own_pull_request',
  'outcome_unknown',
  'forbidden',
  'conflict',
  'not_found',
  'unauthorized',
  'invalid_input',
  'unavailable',
  'not_analysable',
  'internal',
];

export function toActionErrorCode(code: unknown): ActionErrorCode {
  return typeof code === 'string' && KNOWN_CODES.includes(code) ? (code as ActionErrorCode) : 'unknown';
}

/**
 * An action the user asked for failed. `detail` is the server's (or GitHub's) explanation;
 * `outcome_unknown` means the request may have been applied: the user must check GitHub.
 */
export class ActionError extends Error {
  constructor(
    readonly code: ActionErrorCode,
    readonly detail: string = '',
  ) {
    super(detail || code);
    this.name = 'ActionError';
  }
}
