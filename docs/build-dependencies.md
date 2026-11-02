# Build dependency security patches

Builds use Node 24 and `npm ci`. Keep both npm lockfiles with their matching
manifests; do not use force or legacy peer resolution to silence a conflict.

The frontend carries the following bounded compatibility choices while parent
packages retain older ranges:

- `minimatch` 11.0.4 for modern named-API consumers in ESLint 10,
  TypeScript-ESLint, Glob and FileList. The old callable-API consumers are no
  longer in the resolved tree. The unused React plugin registration was removed;
  React Hooks and Fast Refresh checks remain enabled.
- FileList 2.0.2 for Jake's existing FileList API, and Glob 14.0.3 for Workbox's
  existing promise/sync glob API. These versions support the build's Node runtime.
- A local `@jotist/deepmerge@4.3.1-jotist.1` security backport used by Rollup's resolver.
  It filters `__proto__` keys before source and target copying. This closes
  GHSA-gcqx-w48m-w2h4, including null-prototype objects and nested null/primitive
  placeholders; it is not an upstream patched release. The verified original
  archive integrity, source hash, license and exact change are recorded in
  `web/frontend/vendor/deepmerge/UPSTREAM.md`.

`npm test` includes dependency-boundary regressions through the packages loaded
by the actual build consumers: prototype poisoning and normal merge options,
Glob cancellation and normal traversal, and serializer script-boundary escaping.
The normal lint, production/PWA build, responsive browser and image checks remain
required. Remove each override or backport when a maintained parent accepts the
fixed dependency and those checks pass without it.
