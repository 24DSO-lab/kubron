# Contributing to Kubron

Thank you for helping the project. Brief rules below.

## License and contributions

Kubron's core is licensed under [Apache-2.0](LICENSE). By submitting a change you
confirm that you have the right to contribute it and agree to license it under Apache-2.0.

For contributions from external contributors (pull requests opened from a fork), every
commit must include a DCO `Signed-off-by` line. Create signed-off commits with:

```
git commit -s -m "feat: ..."
```

If you forgot, use `git commit --amend -s --no-edit` for the last commit or
`git rebase --signoff <base>` for several commits. CI checks DCO only for pull requests
from forks. Pull requests from repository collaborators do not require sign-off.

No separate CLA is required.

## Source file headers

Every `.go`, `.ts` and `.vue` file must start with the following header:

```
// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0
```

Add automatically: `python3 scripts/license-headers.py --fix`. CI fails without headers.

## UI text and localization

Kubron is multilingual: the app speaks English, and the frontend translates to the selected language.
Full rules and common mistakes are in [docs/i18n.md](docs/i18n.md), architectural decision —
[ADR-0014](docs/adr/0014-i18n.md). Short checklist for every change:

1. **Never hardcode user-facing text.** Not in `<template>`, `.ts`, toasts or
   `confirm`: use a key in `frontend/src/i18n/locales/en/<section>.json` and call `t('section.key')`.
2. **Add keys to all languages** (`en`, `ru`, …) with the same placeholders. English is the source;
   Cyrillic in code outside `locales/` is forbidden (allowed in comments).
3. **Go returns only English text.** A new `errors.New`/`fmt.Errorf` in `internal/` or
   `pkg/` must have a corresponding template in `backend.json` of every language. The backend
   knows nothing about languages and does not format strings per locale; it returns identifiers,
   labels are resolved on the frontend.
4. **Display backend errors via `errorText(e)`**; do not make decisions based on error text.
5. **Dates and numbers through `i18n/format.ts`**, not `toLocaleString('ru-RU')` or manual formatting.
6. **Do not call `t()` at module top level** — only inside functions, `computed`, or render.
7. **Do not translate** Kubernetes object names, YAML fields, API statuses, brand names.
8. **Tests must not depend on Russian text.** Unit tests work with English (the default locale),
   e2e sets the locale explicitly; verify roles / `data-testid` rather than strings.
9. **`v-html` with translations is forbidden**; use `<i18n-t>` for markup within text.
10. When changing visible behavior, consider both sides: what a user with Russian and with
    English interface will see (string length, pluralization).

Check: `npm run i18n:check` in `frontend/` (also a CI step) — key completeness, placeholders,
references to non-existent keys, Cyrillic in code, undeclared Go errors.

A pull request that adds or changes visible text is considered ready only if it includes
translations for all supported languages.

## Dependencies

CI checks dependency licenses: Go — `go-licenses`, frontend — `license-checker`.
Apache-2.0, MIT, BSD-2/3-Clause, ISC are allowed (for Go also MPL-2.0). A dependency with a
different license (including GPL/AGPL/SSPL) cannot be added without discussion.

## Checks before pull request

```
gofmt -l .
go vet ./... && go test ./...
cd frontend && npm test && npm run i18n:check
```

## Branches

Day-to-day work goes to `refactor` (or a feature branch) through a pull request. `dev` holds what is ready for a
release and `master` holds what has been released; do not open pull requests into `master` directly
(see [docs/releasing.md](docs/releasing.md)).

## Architecture decisions

Write a short ADR in [`docs/adr/`](docs/adr/README.md) before or together with the code when a change:

1. is hard or expensive to undo (storage format, schema, a public RPC contract, a hash format);
2. touches several parts of the system (core and both shells, the server and the chart, security);
3. involves a real choice between options that someone may question later;
4. changes or replaces an earlier ADR.

An ADR is a few lines: context, the choice, and what was rejected and why. Do not edit an accepted ADR; write a new
one that supersedes it. Smaller implementation choices go to the pull request description, or to
[`docs/decisions/`](docs/decisions/README.md) when a person has to review them.

## Releases

A release is cut by running `scripts/release.sh X.Y.Z` from the `dev` branch.
Workflow, GitHub setup and troubleshooting: [docs/releasing.md](docs/releasing.md).
User-visible changes should be added to `CHANGELOG.md` under `[Unreleased]`.
