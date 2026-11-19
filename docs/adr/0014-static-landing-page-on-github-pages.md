# ADR-0014: Static Landing Page on GitHub Pages

## Status
Accepted (2026-11-18)

## Context
PR Follower needs a public page that explains what it does and sends people to the repository. The product itself cannot be that page: the panel sits behind GitHub sign-in and ships inside every team's self-hosted image (ADR-0010), so a marketing route there would reach every deployment and fall under the app's i18n, testing and component rules (ADR-0008, ADR-0009).

The page is one long scroll with screenshots and CSS motion. It has no forms, no data and nothing to log in to. Any framework or build would add a lockfile, Dependabot traffic and a toolchain to keep current, for a page that changes a few times a year.

Visitors should also not be tracked by the page: loading fonts or images from a third-party host sends each visitor's IP address there, which is a privacy concern under the GDPR and Brazil's LGPD.

## Decision
1. **A static directory.** The landing page lives in `landing/`: `index.html`, `styles.css`, self-hosted fonts in `landing/fonts/` (with their licences) and WebP screenshots in `landing/images/`. GitHub Pages serves the directory as is; there is no build step.
2. **No JavaScript.** Motion is CSS only (keyframes and scroll-driven animations), and `prefers-reduced-motion` turns it off. Browsers without scroll-driven animations show the page without those effects.
3. **Nothing from another origin.** Fonts, images and styles are served from `landing/`. Links to GitHub are ordinary `<a href>` links, which are not requests.
4. **Deployed from `main`.** `.github/workflows/pages.yml` publishes `landing/` to GitHub Pages when a change to it reaches `main`, so the page describes released code. It can also be run by hand.
5. **English only** for now. A translation would be another static file beside it.

## Consequences
- **Positive:** No dependencies, no build, nothing for Dependabot to bump; the page can be opened straight from disk.
- **Positive:** No visitor request leaves the site, so there is no consent banner to show.
- **Negative:** Shared markup (the GitHub icon, the footer) is repeated by hand instead of coming from components.
- **Negative:** A change on `develop` only goes live with the next release PR to `main` (or a manual run).

## Compliance & Automated Gate
`adrcheck.ValidateRepository` fails, when `landing/` exists, if:
- `landing/index.html` is missing;
- `landing/` contains a `package.json`, a lockfile, `node_modules`, or a `.js`, `.mjs` or `.ts` file;
- an HTML file in `landing/` has a `<script>`;
- an HTML or CSS file in `landing/` loads an `http:`, `https:` or protocol-relative URL through `src`, `srcset`, `poster`, `data`, a `<link href>`, `url(...)` or `@import`. A `<link>` whose `rel` is metadata the browser does not fetch (`canonical`, `alternate`, `me`, `author`, `license`) is not a request, nor is a `<meta>` such as `og:image`.
