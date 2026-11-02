# Deepmerge security backport

Source: official npm `deepmerge@4.3.1` distribution (`dist/cjs.js`).

- Registry archive integrity: `sha512-3sUqbMEc77XqpdNO7FRyRog+eW3ph+GYCbj+rK+uYyRMuwsVy0rMiVtPn+QJlKFvWP/1PYpapqYn0Me2knFn+A==`
- Original source SHA-256: `2e5160f84a1141bd927264afe4d15766f1e6cf07daef182b37ff53fea121de7d`
- Upstream: https://github.com/TehShrike/deepmerge/tree/v4.3.1
- Advisory: https://github.com/advisories/GHSA-gcqx-w48m-w2h4

The local change filters `__proto__` from enumerable keys before either source or target copying, including recursive merges. It prevents the returned-object prototype poisoning reproduced with null/primitive placeholders, null-prototype targets and target-owned `__proto__` keys. Other merge options and ordinary keys keep their upstream behavior. This is a downstream patch, not an upstream fixed release; keep it until a compatible maintained release replaces it. Tests run through the dependency actually resolved by the Rollup consumer.

The private local package is named `@jotist/deepmerge`, version `4.3.1-jotist.1`, to identify the patched downstream source. The npm override redirects the original dependency name to that package; the original unpatched registry package is absent from the selected tree. No advisory IDs are ignored.
