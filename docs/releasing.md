# Выпуск релизов

Релиз выпускается одной командой. Всё остальное (CI, сборка под Windows/Linux/macOS, описание
релиза, публикация) делают GitHub Actions.

## Ветки

| Ветка | Назначение | Защита на GitHub |
|---|---|---|
| `refactor` (или feature-ветки) | рабочая: коммиты из IDE | нет |
| `dev` | готово к выпуску; CI запускается на каждый push | запрет удаления и force-push |
| `master` | выпущенное; на нём ставятся теги `vX.Y.Z` | только PR, 7 обязательных проверок, запрет force-push |

GitHub — основной репозиторий. GitLab (`origin`) — вторичный: туда только отправляем
(`git push origin dev master --tags`, без `--force`), а тянем (`pull`) всегда из `github`.

## Порядок действий

1. **Коммит в `refactor`** из IDE (подпись включена: VS Code `git.alwaysSignOff`, JetBrains
   «Sign-off commit»), затем `git push github refactor`.
2. **CHANGELOG.** Если изменение заметно пользователю, добавьте строку в `CHANGELOG.md` в раздел
   `## [Unreleased]` (Added / Changed / Fixed / Removed / Security). Эти строки станут описанием релиза.
3. **В `dev`.** Когда всё готово к выпуску: слейте `refactor` в `dev` (в IDE: Merge, либо
   `git checkout dev && git merge refactor && git push github dev`). На push в `dev` запускается CI:
   дождитесь зелёного.
4. **Выпуск.** Из `dev`, с чистым рабочим деревом:
   ```bash
   scripts/release.sh 0.2.0 --dry-run   # проверки и предпросмотр описания, ничего не меняется
   scripts/release.sh 0.2.0             # сам выпуск (--watch: дождаться релизного workflow)
   ```
5. **Готово.** Через 10-20 минут релиз опубликован: `https://github.com/24DSO-labs/kubron/releases`.
   После выпуска `refactor` уже равна `master`: продолжайте работу с `git checkout refactor && git pull`.

## Что делает `scripts/release.sh`

1. Проверяет: вы в `dev`, дерево чистое, `dev` совпадает с GitHub, тега нет, подписи DCO в порядке.
2. Переносит `[Unreleased]` в раздел `## [X.Y.Z] - дата`, обновляет ссылки внизу CHANGELOG и версии
   в `wails.json` и `frontend/package*.json`; коммит `chore(release): vX.Y.Z`, push в `dev`.
3. Создаёт pull request `dev` → `master`, ждёт зелёных проверок, сливает его (merge commit).
4. Ставит аннотированный тег `vX.Y.Z` на `master` и отправляет его.
5. Продвигает `dev` и рабочую ветку `refactor` до `master` (быстрая перемотка), чтобы в `refactor`
   был релизный коммит и свёрнутый CHANGELOG. Другие ветки: `RELEASE_SYNC_BRANCHES="refactor feature-x"`,
   отключить: `RELEASE_SYNC_BRANCHES=" "`. Если в ветке есть свои коммиты, она не трогается:
   скрипт подскажет, что слить вручную (`git merge github/master`).
   В этом случае возможен конфликт в `CHANGELOG.md`: оставьте в `[Unreleased]` только свои новые
   строки, а то, что уже свёрнуто в `[X.Y.Z]`, возьмите из `master`.

Если скрипт остановился (красная проверка, обрыв сети), исправьте причину (для проверок:
коммит в `dev` и push) и запустите **ту же команду** снова: готовые шаги пропускаются.

Предрелиз: `scripts/release.sh 0.3.0-rc.1`. CHANGELOG при этом не переносится, описание берётся из
`[Unreleased]`, в файлах версия `0.3.0`, релиз помечается как pre-release.

## Что делает workflow `Release` (запускается на тег `v*.*.*`)

1. Проверяет, что версия тега совпадает с `wails.json` и `frontend/package.json`.
2. Собирает Windows (x64), Linux (x64), macOS (universal).
3. Считает `SHA256SUMS.txt`, берёт описание из `CHANGELOG.md` (раздел версии; для rc без раздела:
   `[Unreleased]`), добавляет таблицу загрузок (`.github/release-footer.md`) и ссылку на сравнение.
4. Создаёт релиз (для `v0.*` и rc: pre-release) и публикует его. Релизы неизменяемые (Immutable):
   файлы и тег после публикации не меняются.

Оставлять релиз черновиком для ручной проверки: переменная репозитория `RELEASE_AUTO_PUBLISH=false`
(Settings → Secrets and variables → Actions → Variables).

## Разовая настройка

1. **GitHub CLI:** `winget install GitHub.cli`, затем `gh auth login`.
2. **Защита `dev`:** в ruleset `protect-main-branches` оставьте целью только `master`; создайте
   ruleset `protect-dev` (Branch, цель `dev`) с правилами Restrict deletions и Block force pushes.
   Обязательный PR и проверки для `dev` не включайте: тогда слияние в IDE работает как в GitLab.
3. **Защита `master`** остаётся как есть. Допустимые способы слияния PR: Merge (скрипт использует его;
   squash ломает проверку DCO, а rebase снимите).
4. **Не выключайте rulesets и не делайте force-push в `master`/`dev`**, в том числе `git push --all --force`
   и `--mirror`: это уже один раз откатило `master` назад.

## Если что-то пошло не так

- **Проверки PR красные:** исправьте в `dev`, `git push github dev`, запустите команду снова.
- **Упал workflow Release:** на странице запуска «Re-run failed jobs». Если упал после создания
  черновика: удалите черновик в Releases и перезапустите запуск.
- **Ошибка в опубликованном релизе:** Immutable-релиз изменить нельзя. Выпустите следующую версию
  (`0.2.1`); править можно только название и описание релиза.
- **Срочное исправление (hotfix):** ветка от `master`, PR в `master`, затем `git merge github/master`
  в `dev` (скрипт требует, чтобы в `dev` были все коммиты `master`).
- **Нет `gh` или не нужен автомат:** вручную то же самое: `node scripts/changelog.mjs roll X.Y.Z`,
  версии в двух файлах, коммит в `dev`, PR `dev` → `master` с merge commit, `git tag -a vX.Y.Z`
  на `master` и `git push github vX.Y.Z`.
