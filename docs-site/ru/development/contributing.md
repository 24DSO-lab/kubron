# Участие в разработке

Перед отправкой изменений прочитайте [CONTRIBUTING.md](https://github.com/24DSO-labs/kubron/blob/master/CONTRIBUTING.md). Ниже — краткое резюме правил.

## DCO

Каждый коммит должен содержать sign-off:

```bash
git commit -s -m "feat: ..."
```

CI проверяет Developer Certificate of Origin.

## Заголовки лицензии

Каждый файл `.go`, `.ts` и `.vue` должен содержать заголовок авторского права и SPDX:

```
// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0
```

## Локализация

- Не добавляйте пользовательский текст напрямую в код.
- Добавляйте ключи во все поддерживаемые языки.
- Backend возвращает английские идентификаторы; frontend подбирает локализованные подписи.
- Запускайте `npm run i18n:check` в каталоге `frontend/`.

## Перед pull request

```bash
gofmt -l .
go vet ./... && go test ./...
cd frontend && npm test && npm run i18n:check
```

Также соблюдайте [Code of Conduct](https://github.com/24DSO-labs/kubron/blob/master/CODE_OF_CONDUCT.md).
