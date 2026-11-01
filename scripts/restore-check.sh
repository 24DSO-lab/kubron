#!/usr/bin/env bash
# Проверка восстановления из резервной копии (docs/backup.md). Те же шаги выполняет job `restore-check`
# в .github/workflows/ci.yml.
#
#   1. сервер на БД src с ключом K1: администратор, команда, пользователь и кластер с секретом (токен
#      в kubeconfig); при работающем сервере снимается pg_dump;
#   2. дамп восстанавливается в пустую БД dst, сервер стартует на dst с тем же ключом K1 и пустым каталогом
#      данных: вход проходит, kubeconfig читается и совпадает с исходным;
#   3. сервер с неверным ключом (другой id и тот же id с другими байтами) не стартует и пишет понятную
#      ошибку без ключа; с KUBRON_ALLOW_KEY_MISMATCH=1 стартует, а секрет не отдаётся;
#   4. ротация: K3 + K1 в KUBRON_SECRET_KEY_PREVIOUS → `rekey` → сервер только с K3 читает тот же секрет.
#
# Нужны: PostgreSQL (клиентские pg_dump, pg_restore, psql, createdb, dropdb, доступ к серверу через переменные
# PGHOST, PGPORT, PGUSER, PGPASSWORD), curl, jq, openssl и собранный kubron-server.
#
# Использование: KUBRON_SERVER_BIN=./kubron-server scripts/restore-check.sh
# Пароль пользователя БД не должен содержать символов, которые надо экранировать в URL.
set -euo pipefail

bin="${KUBRON_SERVER_BIN:-./kubron-server}"
export PGHOST="${PGHOST:-127.0.0.1}" PGPORT="${PGPORT:-5432}" PGUSER="${PGUSER:-postgres}" PGDATABASE="${PGDATABASE:-postgres}"
: "${PGPASSWORD:?set PGPASSWORD (the check connects to PostgreSQL as $PGUSER)}"
export PGPASSWORD

src_db="${RESTORE_CHECK_SRC_DB:-kubron_restore_src}"
dst_db="${RESTORE_CHECK_DST_DB:-kubron_restore_dst}"
port="${RESTORE_CHECK_PORT:-18081}"
base="http://127.0.0.1:$port"

work="$(mktemp -d)"
pid=""
fail() { echo "FAIL: $*" >&2; dump_logs; exit 1; }
dump_logs() { for f in "$work"/*.log; do [ -f "$f" ] && { echo "---- $f"; tail -n 40 "$f"; }; done 2>/dev/null || true; }

stop_server() {
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$pid" 2>/dev/null || true
  fi
  wait "$pid" 2>/dev/null || true
  pid=""
}
cleanup() {
  stop_server
  dropdb --if-exists --force "$src_db" >/dev/null 2>&1 || true
  dropdb --if-exists --force "$dst_db" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

for c in curl jq openssl pg_dump pg_restore psql createdb dropdb; do
  command -v "$c" >/dev/null || fail "$c is required"
done
[ -x "$bin" ] || fail "kubron-server binary not found: $bin (set KUBRON_SERVER_BIN)"

db_url() { echo "postgres://$PGUSER:$PGPASSWORD@$PGHOST:$PGPORT/$1?sslmode=disable"; }
newkey() { echo "$1:$(openssl rand -base64 32)"; } # id:base64 (32 байта)
keybytes() { echo "${1#*:}"; }

key1="$(newkey rc1)"          # ключ, которым зашифрованы данные
key_other_id="$(newkey rc2)"  # другой идентификатор: в связке нет ключа rc1
key_same_id="$(newkey rc1)"   # тот же идентификатор, другие байты
key3="$(newkey rc3)"          # новый ключ для ротации

secret_token="rc-secret-token-$(openssl rand -hex 16)"
kubeconfig="apiVersion: v1
kind: Config
clusters:
- name: rc
  cluster: {server: \"https://restore-check.invalid:6443\", insecure-skip-tls-verify: true}
users:
- name: rc
  user: {token: $secret_token}
contexts:
- name: rc
  context: {cluster: rc, user: rc}
current-context: rc
"

pass="restore-check-pass-1"
tmp_pass="restore-check-temp-1"

# start <имя> <БД> <ключ> [прежний ключ] — фоновый запуск; ждёт готовности или падает с журналом
start_server() {
  local name="$1" db="$2" key="$3" prev="${4:-}"
  mkdir -p "$work/data-$name"
  KUBRON_DATABASE_URL="$(db_url "$db")" KUBRON_SECRET_KEY="$key" KUBRON_SECRET_KEY_PREVIOUS="$prev" \
    KUBRON_DATA_DIR="$work/data-$name" \
    "$bin" --listen "127.0.0.1:$port" >"$work/$name.log" 2>&1 &
  pid=$!
  for _ in $(seq 1 60); do
    curl -fsS "$base/healthz" >/dev/null 2>&1 && return 0
    kill -0 "$pid" 2>/dev/null || fail "server '$name' exited during start"
    sleep 0.5
  done
  fail "server '$name' did not become healthy"
}

# expect_refused <имя> <БД> <ключ> <фрагмент ошибки> — сервер обязан завершиться с ошибкой и понятным текстом
expect_refused() {
  local name="$1" db="$2" key="$3" want="$4" status=0
  mkdir -p "$work/data-$name"
  KUBRON_DATABASE_URL="$(db_url "$db")" KUBRON_SECRET_KEY="$key" KUBRON_DATA_DIR="$work/data-$name" \
    timeout 60 "$bin" --listen "127.0.0.1:$port" >"$work/$name.log" 2>&1 || status=$?
  [ "$status" -ne 0 ] || fail "$name: the server must refuse to start with a wrong key"
  [ "$status" -ne 124 ] || fail "$name: the server hung instead of failing"
  grep -q "KUBRON_SECRET_KEY does not match" "$work/$name.log" || fail "$name: the error must name KUBRON_SECRET_KEY"
  grep -q -- "$want" "$work/$name.log" || fail "$name: the error must mention '$want'"
  grep -q "docs/backup.md" "$work/$name.log" || fail "$name: the error must point to docs/backup.md"
}

# rpc <jar> <метод> <JSON-массив аргументов> — тело ответа в stdout; CSRF-токен берётся из <jar>.csrf
rpc() {
  local jar="$1" method="$2" args="${3:-[]}" csrf=""
  [ -f "$jar.csrf" ] && csrf="$(cat "$jar.csrf")"
  curl -sS -c "$jar" -b "$jar" -D "$jar.hdr" -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf" \
    -d "$args" "$base/api/rpc/$method"
}
# ok <jar> <метод> <аргументы> — как rpc, но ошибка ответа проваливает проверку
ok() {
  local out
  out="$(rpc "$@")"
  [ "$(jq -r '.error // empty' <<<"$out")" = "" ] || fail "$2 failed: $(jq -c .error <<<"$out")"
  echo "$out"
}
save_csrf() { tr -d '\r' <"$1.hdr" | awk -F': ' 'tolower($1)=="x-csrf-token"{print $2}' >"$1.csrf"; }
login() { # <jar> <логин> <пароль...> — первый подходящий пароль
  local jar="$1" user="$2"; shift 2
  local p
  for p in "$@"; do
    rm -f "$jar" "$jar.csrf"
    local code
    rpc "$jar" Login "$(jq -nc --arg u "$user" --arg p "$p" '[$u,$p]')" >/dev/null
    code="$(awk 'NR==1{print $2}' "$jar.hdr")"
    if [ "$code" = "200" ]; then save_csrf "$jar"; return 0; fi
  done
  return 1
}

echo "== 1. data on the source database"
createdb "$src_db"
start_server src "$src_db" "$key1"

admin="$work/admin.jar"; user="$work/user.jar"
out="$(rpc "$admin" SetupAdmin '["rc-admin","'"$pass"'"]')"
code="$(awk 'NR==1{print $2}' "$admin.hdr")"
[ "$code" = "200" ] || fail "SetupAdmin: $out"
save_csrf "$admin"
team_id="$(ok "$admin" CreateTeam '["rc-team"]' | jq -r '.result.id')"
{ [ -n "$team_id" ] && [ "$team_id" != "null" ]; } || fail "CreateTeam returned no id"
ok "$admin" CreateUser "$(jq -nc --arg p "$tmp_pass" '["rc-user",$p,"user"]')" >/dev/null
user_id="$(ok "$admin" ListUsers | jq -r '.result[] | select(.username=="rc-user") | .id')"
[ -n "$user_id" ] || fail "rc-user not found"
login "$user" rc-user "$tmp_pass" || fail "first login of rc-user"
ok "$user" ChangePassword "$(jq -nc --arg a "$tmp_pass" --arg b "$pass" '[$a,$b]')" >/dev/null
ok "$admin" SetTeamMember "$(jq -nc --arg t "$team_id" --arg u "$user_id" '[$t,$u,"admin"]')" >/dev/null
settings='{"displayName":"restore-check-cluster","icon":"","color":"","environment":"","defaultNamespace":"","contextName":""}'
cluster_id="$(ok "$user" AddClusterToTeam "$(jq -nc --arg t "$team_id" --arg k "$kubeconfig" --argjson s "$settings" '[$t,$k,$s]')" | jq -r '.result.id')"
{ [ -n "$cluster_id" ] && [ "$cluster_id" != "null" ]; } || fail "AddClusterToTeam returned no id"

before="$work/kubeconfig.before"
ok "$user" GetClusterKubeconfig "$(jq -nc --arg c "$cluster_id" '[$c]')" | jq -r '.result' >"$before"
grep -q "$secret_token" "$before" || fail "the source server must return the stored kubeconfig with the secret"

echo "== 2. pg_dump of the running server, restore into an empty database"
pg_dump -Fc --no-owner --no-privileges -f "$work/kubron.dump" "$src_db"
stop_server
createdb "$dst_db"
pg_restore --no-owner --no-privileges --exit-on-error -d "$dst_db" "$work/kubron.dump"

echo "== 3. server on the restored database with the same key"
start_server restored "$dst_db" "$key1"
login "$user" rc-user "$pass" || fail "login with the restored data"
teams="$(ok "$user" ListTeams)"
jq -e --arg t "$team_id" '.result[] | select(.id==$t)' <<<"$teams" >/dev/null || fail "the team did not survive the restore"
after="$work/kubeconfig.after"
ok "$user" GetClusterKubeconfig "$(jq -nc --arg c "$cluster_id" '[$c]')" | jq -r '.result' >"$after"
cmp -s "$before" "$after" || fail "the kubeconfig after the restore differs from the original (decryption)"
stop_server

echo "== 4. wrong keys: a clear error, no key in the output"
expect_refused wrong-id "$dst_db" "$key_other_id" "unknown key"
expect_refused wrong-bytes "$dst_db" "$key_same_id" "failed to decrypt"

echo "== 5. KUBRON_ALLOW_KEY_MISMATCH=1: starts, the secret is not served"
KUBRON_ALLOW_KEY_MISMATCH=1 start_server mismatch "$dst_db" "$key_other_id"
login "$user" rc-user "$pass" || fail "users must still log in without the key"
out="$(rpc "$user" GetClusterKubeconfig "$(jq -nc --arg c "$cluster_id" '[$c]')")"
[ "$(jq -r '.error.code // empty' <<<"$out")" != "" ] || fail "the secret must not be served with a wrong key: $out"
grep -q "$secret_token" <<<"$out" && fail "the error response contains the secret"
stop_server

echo "== 6. key rotation: previous key + rekey, then only the new key"
start_server rotated "$dst_db" "$key3" "$key1"
stop_server
rekey_out="$(KUBRON_DATABASE_URL="$(db_url "$dst_db")" KUBRON_SECRET_KEY="$key3" KUBRON_SECRET_KEY_PREVIOUS="$key1" \
  KUBRON_DATA_DIR="$work/data-rekey" "$bin" rekey 2>&1)" || fail "rekey: $rekey_out"
grep -q "re-encrypted with key \"rc3\": 1 kubeconfigs" <<<"$rekey_out" || fail "rekey must re-encrypt the cluster kubeconfig: $rekey_out"
start_server rekeyed "$dst_db" "$key3"
login "$user" rc-user "$pass" || fail "login after rekey"
rotated="$work/kubeconfig.rotated"
ok "$user" GetClusterKubeconfig "$(jq -nc --arg c "$cluster_id" '[$c]')" | jq -r '.result' >"$rotated"
cmp -s "$before" "$rotated" || fail "the kubeconfig after rekey differs from the original"
stop_server

echo "== 7. no secrets in server logs"
for s in "$secret_token" "$(keybytes "$key1")" "$(keybytes "$key_other_id")" "$(keybytes "$key_same_id")" "$(keybytes "$key3")"; do
  ! grep -rqF -- "$s" "$work"/*.log || fail "a secret reached the server log"
done

echo "OK: restore check passed"
