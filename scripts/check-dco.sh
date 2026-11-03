#!/usr/bin/env bash
# DCO: используется CI только для внешних PR, открытых из fork; внутренние PR и релизы не требуют sign-off.
# Использование: scripts/check-dco.sh <база> <голова>   (git commit -s добавляет строку сам)
#
# Коммиты, созданные до DCO_SINCE (по умолчанию 2026-10-30), не проверяются: проверка введена позже,
# а переписывать старую историю не нужно. Начать проверять раньше/позже: DCO_SINCE=2026-11-15 ...
set -euo pipefail
base="${1:?base}"; head="${2:?head}"
since="${DCO_SINCE:-2026-10-30T00:00:00Z}"
since_ts="$(date -u -d "$since" +%s)"
bad=0
for sha in $(git rev-list --no-merges "$base..$head"); do
  [ "$(git show -s --format=%ct "$sha")" -lt "$since_ts" ] && continue
  if ! git show -s --format=%B "$sha" | grep -Eq '^Signed-off-by: .+ <.+@.+>$'; then
    echo "Нет Signed-off-by: $(git show -s --format='%h %s' "$sha")"
    bad=1
  fi
done
if [ "$bad" -ne 0 ]; then
  echo "Подпишите коммиты: git commit -s --amend (или git rebase --signoff $base). См. CONTRIBUTING.md"
  exit 1
fi
