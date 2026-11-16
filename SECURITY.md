# Security Policy

## Reporting a vulnerability

Please report security issues privately through GitHub: on this repository, open **Security → Report a vulnerability**. Do not open a public issue or pull request for a vulnerability.

Include what you found, how to reproduce it, and the version (release tag or commit) you tested. You will get an acknowledgement within a few days. This is a project maintained by one person in their spare time, so fixes are best effort; you will be credited in the release notes unless you prefer not to be.

## Supported versions

Only the latest release receives security fixes. Self-hosted deployments should upgrade to it ([`INSTALL.md`](INSTALL.md) §6).

## Scope

PR Follower runs on your own infrastructure with your own GitHub App. Issues in how it handles GitHub tokens, sessions, webhooks or repository contents are in scope. Misconfiguration of a deployment (for example a missing TLS proxy) and vulnerabilities in GitHub itself are not.

How the project scans itself: [`docs/security.md`](docs/security.md).
