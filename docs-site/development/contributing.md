# Contributing

Thank you for helping Kubron. This page summarizes the rules; the authoritative version is [CONTRIBUTING.md](https://github.com/24DSO-lab/kubron/blob/master/CONTRIBUTING.md) in the repository. Please also follow the [Code of Conduct](https://github.com/24DSO-lab/kubron/blob/master/CODE_OF_CONDUCT.md).

[[toc]]

## Set up

[Build from source](/guide/build-from-source) explains the prerequisites and how to run Kubron in development mode. Read [Architecture](/development/architecture) to find your way around the code.

## License and sign-off

Kubron's code is licensed under [Apache-2.0](https://github.com/24DSO-lab/kubron/blob/master/LICENSE). By submitting a change you confirm that you have the right to contribute it and agree to license it under Apache-2.0. No separate CLA is required.

Pull requests opened **from a fork** need a DCO `Signed-off-by` line in every commit:

```bash
git commit -s -m "feat: ..."
```

If you forgot, `git commit --amend -s --no-edit` fixes the last commit, and `git rebase --signoff <base>` fixes several. CI checks DCO only for pull requests from forks. Pull requests from repository collaborators do not need sign-off.

## Source file headers

Every `.go`, `.ts` and `.vue` file must start with:

```
// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0
```

Add them automatically with `python3 scripts/license-headers.py --fix`. CI fails without headers.

## UI text and localization

Kubron speaks English in the code, and the frontend translates to the selected language (English, Русский and 中文). The rules are in [docs/i18n.md](https://github.com/24DSO-lab/kubron/blob/master/docs/i18n.md). Checklist for every change:

1. **Never hardcode user-facing text.** Add a key to `frontend/src/i18n/locales/en/<section>.json` and call `t('section.key')`.
2. **Add the key to every language** with the same placeholders. English is the source. Cyrillic in code outside `locales/` is forbidden (comments are fine).
3. **Go returns English text only.** A new `errors.New` or `fmt.Errorf` in `internal/` or `pkg/` needs a template in `backend.json` of every language. The backend returns identifiers, and the frontend resolves labels.
4. **Show backend errors through `errorText(e)`** and never branch on error text.
5. **Format dates and numbers through `i18n/format.ts`**, not `toLocaleString('ru-RU')` or by hand.
6. **Do not call `t()` at module top level**, only inside functions, `computed` or render.
7. **Do not translate** Kubernetes object names, YAML fields, API statuses or brand names.
8. **Tests must not depend on Russian text.** Unit tests use English; end-to-end tests set the locale and check roles or `data-testid`.
9. **No `v-html` with translations.** Use `<i18n-t>` for markup inside text.
10. Consider how a Russian and an English interface will look (string length, plurals).

A pull request that adds or changes visible text is ready only when it includes translations for all supported languages.

## Dependencies

CI checks dependency licenses (`go-licenses` for Go, `license-checker` for the frontend). Apache-2.0, MIT, BSD-2/3-Clause and ISC are allowed, and MPL-2.0 for Go. A dependency under any other license, including GPL, AGPL and SSPL, cannot be added without a discussion.

## Before a pull request

```bash
gofmt -l .
go vet ./... && go test ./...
cd frontend && npm test && npm run i18n:check
```

Also follow the [Code of Conduct](https://github.com/24DSO-lab/kubron/blob/master/CODE_OF_CONDUCT.md).

`npm run i18n:check` verifies key completeness, placeholders, references to missing keys, Cyrillic in code and undeclared Go errors.

## Branches

Day-to-day work goes to `refactor` (or a feature branch) through a pull request. `dev` holds what is ready for a release, and `master` holds what has been released. **Do not open pull requests into `master` directly.** See [Releasing](/development/releasing).

## Architecture decisions

Write a short ADR in [`docs/adr/`](https://github.com/24DSO-lab/kubron/blob/master/docs/adr/README.md) before or together with the code when a change:

1. is hard or expensive to undo (a storage format, a schema, a public RPC contract, a hash format);
2. touches several parts of the system (the core and both shells, the server and the chart, security);
3. involves a real choice between options that someone may question later;
4. changes or replaces an earlier ADR.

An ADR is a few lines: context, the choice, and what was rejected and why. Do not edit an accepted ADR; write a new one that supersedes it. Smaller implementation choices go in the pull request description, or in [`docs/decisions/`](https://github.com/24DSO-lab/kubron/blob/master/docs/decisions/README.md) when a person has to review them.

## Changelog

Add user-visible changes to `CHANGELOG.md` under `[Unreleased]`. They become the release notes.
