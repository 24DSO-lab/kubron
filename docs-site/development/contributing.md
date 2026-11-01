# Contributing

Please read the full [CONTRIBUTING.md](https://github.com/24DSO-labs/kubron/blob/master/CONTRIBUTING.md) in the repository. Short summary:

## DCO

Every commit must be signed off:

```bash
git commit -s -m "feat: ..."
```

CI checks the Developer Certificate of Origin.

## License headers

Every `.go`, `.ts` and `.vue` file must start with:

```
// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0
```

## i18n rules

- Never hardcode user-facing text
- Add keys to all supported languages
- Backend returns English identifiers; frontend resolves labels
- Run `npm run i18n:check` in `frontend/`

## Before a PR

```bash
gofmt -l .
go vet ./... && go test ./...
cd frontend && npm test && npm run i18n:check
```

Also follow the [Code of Conduct](https://github.com/24DSO-labs/kubron/blob/master/CODE_OF_CONDUCT.md).
