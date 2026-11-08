#!/usr/bin/env bash
# Выпуск релиза в два шага (в Git Bash / терминале IDE):
#
#   1. В рабочей ветке (refactor), ДО слияния в dev:
#        scripts/release.sh 0.2.0 --prepare   # [Unreleased] -> [0.2.0], версии в файлах, один коммит; без push
#      Затем: push refactor, проверка, слияние в dev, ожидание зелёного dev.
#   2. В ветке dev, когда dev зелёный:
#        scripts/release.sh 0.2.0            # PR dev -> master, слияние, тег vX.Y.Z; новых коммитов нет
#        scripts/release.sh 0.2.0 --watch    # то же и дождаться релизного workflow
#
#   --dry-run добавляется к любому шагу: только проверки и предпросмотр, ничего не меняется.
#
# Шаг 2 ничего не коммитит и не пушит в dev: в master уходит ровно то содержимое, которое уже
# проверено в dev, поэтому CI не повторяется (см. «Почему часть job'ов пропущена» в docs/releasing.md).
# Шаг 2 -> pull request dev -> master -> ждёт зелёных проверок и сливает его (merge commit) ->
# ставит и отправляет тег vX.Y.Z -> подтягивает dev и рабочие ветки к master. Дальше всё делает workflow Release.
# Если скрипт остановился (например, красная проверка), исправьте причину и запустите ту же
# команду снова: уже сделанные шаги пропускаются. Подробности: docs/releasing.md
#
# Нужно: git, node, gh (GitHub CLI, `gh auth login`). Другой remote: RELEASE_REMOTE=имя.
set -euo pipefail

die()  { echo "ОШИБКА: $*" >&2; exit 1; }
step() { echo; echo "==> $*"; }
info() { echo "    $*"; }

version=""; dry=0; watch=0; prepare=0
for a in "$@"; do
  case "$a" in
    --dry-run) dry=1 ;;
    --watch)   watch=1 ;;
    --prepare) prepare=1 ;;
    -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
    -*)        die "неизвестный параметр $a" ;;
    *)         [ -z "$version" ] || die "версия указана дважды"; version="${a#v}" ;;
  esac
done
[ -n "$version" ] || die "укажите версию: scripts/release.sh 0.2.0"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die "версия «$version» должна быть вида X.Y.Z или X.Y.Z-rc.1"
base_version="${version%%-*}"   # в файлах (wails.json, package.json) хранится версия без суффикса -rc.N
pre=0; [[ "$version" == *-* ]] && pre=1   # предрелиз (rc): CHANGELOG.md не переносится, описание из [Unreleased]
tag="v$version"

cd "$(git rev-parse --show-toplevel)"
for tool in git node gh; do command -v "$tool" >/dev/null || die "не найден $tool"; done

# remote с GitHub: имя берём из RELEASE_REMOTE или ищем по адресу
remote="${RELEASE_REMOTE:-}"
if [ -z "$remote" ]; then
  remote="$(git remote -v | awk '/github\.com/ && /\(push\)/ {print $1; exit}')"
fi
[ -n "$remote" ] || die "не найден remote с github.com (укажите RELEASE_REMOTE=имя)"
slug="$(git remote get-url "$remote" | sed -E 's#^.*github\.com[:/]##; s#\.git$##')"
info "репозиторий: $slug (remote «$remote»), версия: $version"

step "Проверки"
branch="$(git rev-parse --abbrev-ref HEAD)"
if [ "$prepare" -eq 1 ]; then
  case "$branch" in
    master|dev) die "--prepare выполняется в рабочей ветке (refactor), до слияния в dev (сейчас: $branch)" ;;
  esac
else
  gh auth status >/dev/null 2>&1 || die "gh не авторизован: выполните gh auth login"
  [ "$branch" = "dev" ] || die "релиз выпускается из ветки dev (сейчас: $branch)"
fi
[ -z "$(git status --porcelain)" ] || die "есть незакоммиченные изменения: закоммитьте или уберите их"
git fetch --quiet --tags "$remote" || die "git fetch $remote не удался. Частая причина — локальный тег расходится с тегом на GitHub: сравните (git ls-remote --tags $remote), удалите локальный тег (git tag -d vX.Y.Z) и запустите снова"
if [ "$prepare" -eq 0 ]; then
  [ "$(git rev-parse HEAD)" = "$(git rev-parse "$remote/dev")" ] || die "локальная dev не совпадает с $remote/dev: сделайте pull/push"
fi
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null || git ls-remote --exit-code --tags "$remote" "refs/tags/$tag" >/dev/null 2>&1; then
  die "тег $tag уже существует"
fi
git merge-base --is-ancestor "$remote/master" HEAD || die "в $remote/master есть коммиты, которых нет в $branch: сначала git merge $remote/master"
node scripts/changelog.mjs check || die "CHANGELOG.md составлен неверно (см. выше)"
info "окружение, ветка и версии в порядке"

# Версия в файлах (wails.json, frontend/package.json) уже base_version?
files_versioned=0
if grep -q "\"productVersion\"[[:space:]]*:[[:space:]]*\"$base_version\"" wails.json \
  && node -e 'process.exit(require("./frontend/package.json").version === process.argv[1] ? 0 : 1)' "$base_version"; then
  files_versioned=1
fi

# Подготовка уже сделана? Релиз: раздел [X.Y.Z] в CHANGELOG и версии в файлах; предрелиз: только версии в файлах.
already_rolled=0
if [ "$pre" -eq 1 ]; then
  [ "$files_versioned" -eq 1 ] && already_rolled=1
elif node scripts/changelog.mjs has "$version" >/dev/null 2>&1 && [ "$files_versioned" -eq 1 ]; then
  already_rolled=1
fi

if [ "$prepare" -eq 0 ]; then
  # Релиз: ничего не коммитим. Всё, что войдёт в тег, уже должно лежать в dev и быть проверено там.
  [ "$already_rolled" -eq 1 ] || die "в dev нет подготовки релиза $tag (раздел [$version] в CHANGELOG.md и версии в wails.json и frontend/package.json). Выполните в refactor: scripts/release.sh $version --prepare, затем слейте refactor в dev и дождитесь зелёного dev"
  info "подготовка релиза $tag найдена в dev: новых коммитов не будет"
  echo "    Описание релиза будет таким:"
  node scripts/changelog.mjs notes "$version" --fallback-unreleased | sed 's/^/      | /' || die "в CHANGELOG.md нет описания $tag"
  if [ "$dry" -eq 1 ]; then
    echo; echo "Пробный запуск (--dry-run): ничего не изменено."; exit 0
  fi
else
  if [ "$already_rolled" -eq 1 ]; then
    info "подготовка $tag уже сделана (CHANGELOG и версии в файлах): делать нечего"
    exit 0
  fi
  if [ "$pre" -eq 1 ]; then
    info "предрелиз: CHANGELOG.md не меняется, описание берётся из [Unreleased]"
    echo "    Описание релиза будет таким:"
    node scripts/changelog.mjs notes "$version" --fallback-unreleased | sed 's/^/      | /' || die "в [Unreleased] нет изменений"
  else
    if node scripts/changelog.mjs has "$version" >/dev/null 2>&1; then
      die "раздел [$version] в CHANGELOG.md уже есть, но версии в файлах другие: исправьте wails.json и frontend/package.json вручную"
    fi
    tmp="$(mktemp)"; cp CHANGELOG.md "$tmp"
    node scripts/changelog.mjs roll "$version" --file "$tmp" || { rm -f "$tmp"; die "в [Unreleased] нет изменений"; }
    echo "    Описание релиза будет таким:"
    node scripts/changelog.mjs notes "$version" --file "$tmp" | sed 's/^/      | /'
    rm -f "$tmp"
  fi
  if [ "$dry" -eq 1 ]; then
    echo; echo "Пробный запуск (--dry-run): ничего не изменено."; exit 0
  fi

  step "Подготовка релиза: CHANGELOG и версии"
  [ "$pre" -eq 1 ] || node scripts/changelog.mjs roll "$version"
  # shellcheck disable=SC2016  # JS-код в одинарных кавычках: подстановки bash здесь не нужны
  node -e '
    const fs = require("fs");
    const v = process.argv[1];
    const t = fs.readFileSync("wails.json", "utf8");
    const out = t.replace(/("productVersion"\s*:\s*")[^"]*(")/, "$1" + v + "$2");
    if (out === t && !t.includes("\"" + v + "\"")) { console.error("не найден productVersion в wails.json"); process.exit(1); }
    fs.writeFileSync("wails.json", out);
  ' "$base_version"
  npm --prefix frontend version "$base_version" --no-git-tag-version --allow-same-version >/dev/null
  git add CHANGELOG.md wails.json frontend/package.json frontend/package-lock.json
  git commit -s -q -m "chore(release): $tag"
  info "коммит chore(release): $tag создан в $branch (ничего не отправлялось)"
  echo
  echo "Дальше: git push $remote $branch -> проверка -> слияние $branch в dev -> дождаться зелёного dev ->"
  echo "        git checkout dev && scripts/release.sh $version"
  exit 0
fi

step "Pull request dev -> master"
pr="$(gh pr list --repo "$slug" --base master --head dev --state open --json number --jq '.[0].number // empty')"
if [ -z "$pr" ]; then
  body="$(mktemp)"
  node scripts/changelog.mjs notes "$version" --fallback-unreleased > "$body"
  gh pr create --repo "$slug" --base master --head dev --title "Release $tag" --body-file "$body" >/dev/null
  rm -f "$body"
  pr="$(gh pr list --repo "$slug" --base master --head dev --state open --json number --jq '.[0].number')"
fi
info "PR #$pr"

step "Ждём проверки CI (обычно 10-20 минут)"
for _ in $(seq 1 18); do   # проверки появляются не сразу после создания PR
  [ "$(gh pr view "$pr" --repo "$slug" --json statusCheckRollup --jq '.statusCheckRollup | length')" -gt 0 ] && break
  sleep 10
done
gh pr checks "$pr" --repo "$slug" --watch --interval 20 || die "проверки PR #$pr не прошли: исправьте в dev и запустите команду снова"

step "Слияние PR #$pr (merge commit)"
gh pr merge "$pr" --repo "$slug" --merge

step "Тег $tag"
git fetch --quiet "$remote"
git tag -a "$tag" -m "Kubron $version" "$remote/master"
git push "$remote" "$tag"

step "Выравниваем dev по master"
if git merge --ff-only "$remote/master" >/dev/null 2>&1 && git push "$remote" dev >/dev/null 2>&1; then
  info "dev продвинута до $(git rev-parse --short HEAD)"
else
  info "dev не выровнена автоматически: выполните git merge $remote/master && git push $remote dev"
fi

# Релизный коммит и слияние PR появляются только в dev/master. Рабочие ветки (по умолчанию refactor)
# продвигаем до master, иначе их [Unreleased] разойдётся с CHANGELOG из master и будет конфликт.
# Другие ветки: RELEASE_SYNC_BRANCHES="refactor feature-x"; отключить: RELEASE_SYNC_BRANCHES=" ".
step "Выравниваем рабочие ветки по master"
for b in ${RELEASE_SYNC_BRANCHES-refactor}; do
  git ls-remote --exit-code --heads "$remote" "$b" >/dev/null 2>&1 || { info "$b: на $remote такой ветки нет, пропускаем"; continue; }
  if git merge-base --is-ancestor "$remote/$b" "$remote/master"; then
    git push --quiet "$remote" "$remote/master:refs/heads/$b"
    info "$b на $remote продвинута до master"
    if git fetch --quiet "$remote" "master:$b" 2>/dev/null; then
      info "$b локально обновлена"
    else
      info "$b локально обновите сами: git checkout $b && git pull"
    fi
  else
    info "в $b есть коммиты, которых нет в master: git checkout $b && git merge $remote/master"
  fi
done

echo
echo "Тег $tag отправлен. Релизный workflow: https://github.com/$slug/actions/workflows/release.yml"
echo "Страница релиза: https://github.com/$slug/releases/tag/$tag"
if [ "$watch" -eq 1 ]; then
  step "Ждём релизный workflow"
  sleep 15
  run_id="$(gh run list --repo "$slug" --workflow release.yml --event push --limit 10 --json databaseId,headBranch --jq "map(select(.headBranch==\"$tag\"))[0].databaseId // empty")"
  [ -n "$run_id" ] || die "не нашёл запуск workflow для $tag: смотрите вкладку Actions"
  gh run watch "$run_id" --repo "$slug" --exit-status
  echo "Релиз готов: https://github.com/$slug/releases/tag/$tag"
fi
