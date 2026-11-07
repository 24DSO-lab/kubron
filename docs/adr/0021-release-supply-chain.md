# ADR-0021: Выпуск и цепочка поставки: один тег, подписанные образ и чарт, сканеры в CI

- Статус: принято
- Дата: 2026-11-05 (оформлен ретроспективно; публикация образа и чарта появилась в 0.5.0, подпись, SBOM и ворота Trivy описаны в CHANGELOG для 1.0.0, вызов `publish.yml` из `release.yml` — там же)
- Связано: ADR-0016 (откат и совместимость схемы); `docs/releasing.md`, `docs/deploy.md` («Проверка образа и чарта»), `docs/security-review.md`, `.github/workflows/release.yml`, `publish.yml`, `ci.yml`, `deploy.yml`, `scheduled-scan.yml`

## Контекст

Kubron поставляется в двух видах: десктопные файлы (Windows, Linux, macOS) и сервер (образ `ghcr.io/24DSO-lab/kubron`
и Helm-чарт `oci://ghcr.io/24DSO-lab/charts/kubron`). Нужно, чтобы выпуск не зависел от ручных шагов, версия была одна
на все артефакты, а получатель мог проверить происхождение и состав. Исходно `publish.yml` сам реагировал на тег; из
CHANGELOG (0.6.0 → 1.0.0) видно, что это заменили вызовом после CI, чтобы SBOM попадали в неизменяемый релиз, а образ не
публиковался из непроверенного коммита.

## Решение

1. **Выпуск одной командой.** `scripts/release.sh X.Y.Z` (из `dev`, с `--dry-run` для предпросмотра) переносит
   `[Unreleased]` в CHANGELOG, обновляет версии в `wails.json` и `frontend/package*.json`, сливает PR `dev` → `master` и
   ставит аннотированный тег `vX.Y.Z`. Дальше работают GitHub Actions.
2. **Один номер версии.** Тег задаёт версию десктопа, образа, чарта (`version` и `appVersion`) и имён SBOM. `release.yml`
   и `publish.yml` падают, если тег не совпадает с `wails.json` и `frontend/package.json`. В `Chart.yaml` лежит
   `0.0.0-dev`, версию подставляет `helm package`.
3. **Порядок в `release.yml`.** `verify` (полный `ci.yml` как reusable workflow) → параллельно `build` (десктоп) и `server`
   (`publish.yml` с `push: true`) → `release` (SBOM десктопа, `SHA256SUMS.txt`, описание из CHANGELOG, черновик и затем
   публикация). Релиз неизменяемый; `RELEASE_AUTO_PUBLISH=false` оставляет черновик.
4. **`publish.yml`.** Сборка образа → Trivy (HIGH/CRITICAL с исправлением, `exit-code: 1`) → SBOM образа (syft, SPDX) →
   push → подпись cosign keyless по digest → аттестации происхождения и SBOM (`actions/attest-*`) → пакет чарта, push,
   SBOM (CycloneDX, `scripts/chart-sbom.py`), подпись и аттестации. `latest` только у версий без суффикса. Опубликованную
   версию workflow не перезаписывает, а падает. Ручной запуск без тега по умолчанию без push (`0.0.0-dev.<sha>`).
5. **Сканеры и политики.**
   - `ci.yml`: `deps-audit` блокирует (`npm audit --omit=dev --audit-level=high` для `frontend` и `docs-site`,
     osv-scanner с `osv-scanner.toml`, `scripts/check-ignores.py`); `vulncheck` (govulncheck) не блокирует
     (`continue-on-error`), но его отчёт проверяется по чек-листу перед тегом.
   - `deploy.yml`: hadolint для `Dockerfile` (`.hadolint.yaml`, порог `warning`, отключён только DL3008 с пояснением),
     Trivy для образа (отчёт) и отрендеренного чарта (`trivy config`, отчёт).
   - `scheduled-scan.yml`: раз в неделю Trivy по `:latest`, находки попадают в issue с меткой `security`.
   - Отдельно работают `codeql.yml` и `scorecard.yml`; образ собирается на distroless `nonroot`.
6. **Исключения.** Допускаются только в `.trivyignore` и `osv-scanner.toml`; у записи обязательны срок (не дальше 180 дней,
   просроченная запись роняет CI) и обоснование над ней. Формат проверяет `scripts/check-ignores.py`; по
   `docs/releasing.md` исключение подтверждает второй человек в PR. На момент записи в обоих файлах только закомментированные
   примеры, действующих исключений нет.
7. **Проверка получателем.** `gh attestation verify` (происхождение, SPDX и CycloneDX) и `cosign verify` с личностью
   `publish.yml@refs/tags/v…` (`docs/deploy.md`).

## Рассмотренные альтернативы

Рассмотренные альтернативы и причины отказа в репозитории не зафиксированы. По истории видна только одна смена:
собственный триггер `publish.yml` на тег заменён вызовом из `release.yml` после CI (причины в CHANGELOG, см. выше).
Подпись с долгоживущим ключом и иные сканеры в репозитории не обсуждаются.

## Последствия

- Исправление опубликованной версии — только новая версия; частично опубликованную версию удаляют вручную в Packages
  (`docs/releasing.md`).
- Тег проходит полный CI, поэтому выпуск занимает 10–20 минут; отметки «содержимое уже проверено» на теге не действуют.
- Trivy-ворота зависят от базы уязвимостей на момент выпуска; уязвимости, найденные позже, ловит только недельный скан.
- Проверка подписи привязана к имени workflow `publish.yml` и к тегу: переименование файла сломает политики
  admission-контроллеров у получателей. Подпись и аттестации привязаны к digest.
- Таблица проверок в чек-листе перед тегом ведётся вручную в `docs/releasing.md` и может разойтись с `ci.yml`.
