#!/usr/bin/env bash
# Проверка Helm-чарта deploy/helm/kubron: lint, рендер всех наборов values из ci/, схемы Kubernetes
# (kubeconform) и смысловые проверки шаблонов. Те же шаги выполняет job `chart` в
# .github/workflows/deploy.yml.
#
# Нужны helm 3.x/4.x; kubeconform — по желанию (без него проверка схем пропускается).
# Использование: scripts/check-chart.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
chart="$root/deploy/helm/kubron"
k8s_version="${K8S_SCHEMA_VERSION:-1.31.0}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
render() { helm template test "$chart" --namespace default "$@"; }

echo "== helm lint"
for f in "$chart"/ci/*-values.yaml; do
  echo "-- $(basename "$f")"
  helm lint "$chart" --strict -f "$f"
done

echo "== helm template (+ kubeconform)"
for f in "$chart"/ci/*-values.yaml; do
  name="$(basename "$f" .yaml)"
  render -f "$f" > "$tmp/$name.yaml"
  if command -v kubeconform >/dev/null; then
    # ServiceMonitor — CRD Prometheus Operator: схемы берутся из каталога CRD
    kubeconform -strict -summary -kubernetes-version "$k8s_version" -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
      "$tmp/$name.yaml"
  else
    echo "kubeconform not found: schema validation of $name skipped"
  fi
done

# Смысловые проверки: не только «рендерится», но и «рендерится как задумано»
min="$tmp/minimal-values.yaml"
ing="$tmp/ingress-values.yaml"
inc="$tmp/incluster-values.yaml"
rep="$tmp/replicas-values.yaml"
single="$tmp/single-values.yaml"
dev="$tmp/dev-secret-values.yaml"
met="$tmp/metrics-values.yaml"
logv="$tmp/logging-values.yaml"
opr="$tmp/rbac-operator-values.yaml"
cus="$tmp/rbac-custom-values.yaml"
res="$tmp/restricted-values.yaml"
has() { grep -qE -- "$2" "$1" || fail "$3"; }
hasnt() { ! grep -qE -- "$2" "$1" || fail "$3"; }

echo "== semantic checks"
# PodDisruptionBudget только при replicaCount > 1
hasnt "$single" '^kind: PodDisruptionBudget' "PDB rendered with replicaCount=1"
has   "$rep" '^kind: PodDisruptionBudget' "PDB missing with replicaCount=2"
# HA по умолчанию (ADR-0016): две реплики, PDB, размещение по узлам, пауза перед остановкой; предупреждения нет
has   "$min" 'replicas: 2' "default replicaCount must be 2"
has   "$min" '^kind: PodDisruptionBudget' "PDB missing with the default replicaCount"
has   "$min" 'podAntiAffinity' "replicas must prefer different nodes by default"
has   "$min" 'KUBRON_DRAIN_DELAY: "5s"' "drain delay missing by default"
hasnt "$min" 'KUBRON_DB_LISTEN_URL' "listen URL must not be set by default"
hasnt "$min" 'KUBRON_AUDIT_RETENTION' "audit retention must be off by default"
render -f "$chart/ci/minimal-values.yaml" --set secrets.keys.dbListenUrl=KUBRON_DB_LISTEN_URL | grep -q 'name: KUBRON_DB_LISTEN_URL' \
  || fail "secrets.keys.dbListenUrl must add KUBRON_DB_LISTEN_URL from the Secret"
render -f "$chart/ci/minimal-values.yaml" --set kubron.auditRetention=2160h | grep -q 'KUBRON_AUDIT_RETENTION: "2160h"' \
  || fail "kubron.auditRetention must set KUBRON_AUDIT_RETENTION"
! grep -q 'НЕ поддерживаются' "$chart/templates/NOTES.txt" || fail "NOTES.txt must not warn that several replicas are unsupported"
# Ingress только по флагу; TLS ingress включает доверие к прокси автоматически
hasnt "$min" '^kind: Ingress$' "Ingress rendered by default"
has   "$ing" '^kind: Ingress$' "Ingress missing when enabled"
has   "$min" 'KUBRON_TRUST_PROXY: "0"' "trustProxy must be off without ingress TLS"
has   "$ing" 'KUBRON_TRUST_PROXY: "1"' "trustProxy must be on with ingress TLS"
render -f "$chart/ci/ingress-values.yaml" --set kubron.trustProxy=false | grep -q 'KUBRON_TRUST_PROXY: "0"' \
  || fail "explicit kubron.trustProxy=false must win over auto"
# Port-forward в браузере (ADR-0017): без домена потоков нет ни переменной, ни wildcard-хоста
pfv="$tmp/portforward-values.yaml"
hasnt "$min" 'KUBRON_PF_BASE_DOMAIN' "the port-forward domain must be unset by default"
hasnt "$ing" 'host: "\*\.pf\.' "the wildcard stream host must not be rendered without portForward.baseDomain"
has   "$pfv" 'KUBRON_PF_BASE_DOMAIN: "kubron.example.com"' "portForward.baseDomain must set KUBRON_PF_BASE_DOMAIN"
has   "$pfv" 'KUBRON_PF_IDLE_TIMEOUT: "10m"' "portForward.idleTimeout must set KUBRON_PF_IDLE_TIMEOUT"
has   "$pfv" 'host: "\*\.pf\.kubron\.example\.com"' "the Ingress must route the wildcard stream host"
# Токен ServiceAccount только в режиме in-cluster; RBAC только по флагу
has   "$min" 'automountServiceAccountToken: false' "token must not be mounted by default"
hasnt "$min" 'automountServiceAccountToken: true' "token mounted by default"
has   "$inc" 'automountServiceAccountToken: true' "token must be mounted in in-cluster mode"
hasnt "$min" '^kind: ClusterRole$' "RBAC rendered by default"
has   "$inc" '^kind: ClusterRole$' "ClusterRole missing with rbac.create"
# (имя Secret в рендере тоже содержит «secrets», поэтому ищем именно элемент списка resources)
hasnt "$inc" '^[[:space:]]*-[[:space:]]+secrets[[:space:]]*$' "the read-only ClusterRole must not grant access to secrets"
hasnt "$inc" '^[[:space:]]*-[[:space:]]+(create|update|patch|delete|deletecollection|\*)[[:space:]]*$' "the read-only ClusterRole grants write verbs"
# Безопасность пода
for f in "$min" "$inc"; do
  has "$f" 'readOnlyRootFilesystem: true' "readOnlyRootFilesystem missing"
  has "$f" 'allowPrivilegeEscalation: false' "allowPrivilegeEscalation missing"
  has "$f" 'runAsNonRoot: true' "runAsNonRoot missing"
  has "$f" 'type: RuntimeDefault' "seccompProfile RuntimeDefault missing"
done
# Секреты: ключ и строка подключения только из Secret, значений в рендере нет
has   "$min" 'secretKeyRef' "secrets must be referenced from a Secret"
hasnt "$min" '^kind: Secret$' "Secret rendered without secrets.create"
has   "$dev" '^kind: Secret$' "Secret missing with secrets.create"
has   "$dev" 'helm.sh/resource-policy: keep' "dev Secret must survive uninstall"
# Writable-каталоги под emptyDir (HOME и TMPDIR)
has   "$min" 'mountPath: /home/nonroot' "HOME emptyDir missing"
has   "$min" 'mountPath: /tmp' "TMPDIR emptyDir missing"
# Пробы: liveness и startup — /healthz (без БД), readiness — /readyz (проверка БД, draining)
has   "$min" 'path: /healthz' "liveness/startup probes must use /healthz"
has   "$min" 'path: /readyz' "readiness probe must use /readyz"
# Метрики: по умолчанию выключены; включённые живут на отдельном порту, вне основного Service и Ingress
hasnt "$min" 'KUBRON_METRICS_LISTEN' "metrics enabled by default"
hasnt "$min" 'name: metrics' "metrics port/Service rendered by default"
hasnt "$min" '^kind: ServiceMonitor' "ServiceMonitor rendered by default"
has   "$met" 'KUBRON_METRICS_LISTEN: "0.0.0.0:9090"' "metrics listener missing when enabled"
has   "$met" 'name: .*-metrics$' "separate metrics Service missing"
has   "$met" '^kind: ServiceMonitor' "ServiceMonitor missing when enabled"
svc="$(render -f "$chart/ci/metrics-values.yaml" --show-only templates/service.yaml)"
! grep -q 'metrics' <<<"$svc" || fail "the main Service must not expose the metrics port"
np="$(render -f "$chart/ci/metrics-values.yaml" --show-only templates/networkpolicy.yaml)"
grep -q 'port: metrics' <<<"$np" || fail "NetworkPolicy rule for the metrics port missing"
# правило для порта метрик идёт отдельным пунктом и не берёт источники правила основного порта
rule="$(grep -A8 'port: metrics' <<<"$np")"
! grep -q 'ingress-nginx' <<<"$rule" || fail "the metrics port must not be reachable from the ingress namespace"
# Журнал: по умолчанию ничего не добавляется; аудит в stdout и уровень включаются значениями
hasnt "$min" 'KUBRON_AUDIT_STDOUT' "audit to stdout enabled by default"
hasnt "$min" 'KUBRON_LOG_LEVEL' "log level set by default"
has   "$logv" 'KUBRON_AUDIT_STDOUT: "1"' "auditStdout must set KUBRON_AUDIT_STDOUT"
has   "$logv" 'KUBRON_LOG_LEVEL: "debug"' "logLevel must set KUBRON_LOG_LEVEL"

# Допустимые Host (KUBRON_ALLOWED_HOSTS): из ingress.hosts, *.pf.<baseDomain> и kubron.allowedHosts; без источников переменной нет
hasnt "$min" 'KUBRON_ALLOWED_HOSTS' "allowed hosts must be unset without ingress, port-forward or kubron.allowedHosts"
has   "$ing" 'KUBRON_ALLOWED_HOSTS: "kubron\.example\.com"' "allowed hosts must list the ingress hosts"
has   "$pfv" 'KUBRON_ALLOWED_HOSTS: "kubron\.example\.com,\*\.pf\.kubron\.example\.com"' "allowed hosts must add the port-forward wildcard"
has   "$res" 'KUBRON_ALLOWED_HOSTS: "kubron\.internal\.example"' "kubron.allowedHosts must reach KUBRON_ALLOWED_HOSTS"
# Политика адресов кластера: по умолчанию выбор сервера (переменная не задаётся), значения чарта пробрасываются
hasnt "$min" 'KUBRON_CLUSTER_ADDR_POLICY' "the cluster address policy must be left to the server default"
has   "$res" 'KUBRON_CLUSTER_ADDR_POLICY: "strict"' "kubron.clusterAddrPolicy must set KUBRON_CLUSTER_ADDR_POLICY"
has   "$res" 'KUBRON_CLUSTER_ADDR_ALLOW_CIDRS: "127\.0\.0\.0/8"' "kubron.clusterAddrAllowCidrs must set KUBRON_CLUSTER_ADDR_ALLOW_CIDRS"
# NetworkPolicy: egress к API-серверам по умолчанию — любые адреса, кроме link-local и метаданных (v4 и v6 раздельно)
np_min="$(render -f "$chart/ci/minimal-values.yaml" --show-only templates/networkpolicy.yaml)"
has   <(echo "$np_min") 'cidr: ["'"'"']?0\.0\.0\.0/0' "the default API egress must allow IPv4 destinations"
has   <(echo "$np_min") 'cidr: ["'"'"']?::/0' "the default API egress must allow IPv6 destinations"
has   <(echo "$np_min") '169\.254\.0\.0/16' "the default API egress must exclude link-local and metadata (IPv4)"
has   <(echo "$np_min") 'fd00:ec2::254/128' "the default API egress must exclude the AWS IPv6 metadata address"
has   <(echo "$np_min") 'namespaceSelector: \{\}' "the default API egress must still reach pods of the cluster (allowCluster)"
has   <(echo "$np_min") 'port: 53' "DNS egress missing"
has   <(echo "$np_min") 'port: 5432' "PostgreSQL egress missing"
# IPv4-исключение только в блоке IPv4, IPv6 — только в блоке IPv6 (Kubernetes отклоняет исключение чужого семейства)
v4="$(awk '/cidr: ["\047]?0\.0\.0\.0\/0/{f=1;next} /cidr: ["\047]?::\/0/{f=0} f' <<<"$np_min")"
! grep -q 'fd00:ec2' <<<"$v4" || fail "an IPv6 exception must not sit in the IPv4 ipBlock"
# Свои CIDR заменяют «любые адреса»: исключений и 0.0.0.0/0 нет, порт IdP отдельным правилом, PostgreSQL сужен
np_res="$(render -f "$chart/ci/restricted-values.yaml" --show-only templates/networkpolicy.yaml)"
has   <(echo "$np_res") 'cidr: ["'"'"']?10\.20\.0\.0/16' "clusterApi.cidrs must be rendered"
has   <(echo "$np_res") 'cidr: ["'"'"']?10\.30\.0\.0/24' "postgres.cidrs must be rendered"
has   <(echo "$np_res") 'cidr: ["'"'"']?10\.40\.0\.0/24' "idp.cidrs must be rendered"
has   <(echo "$np_res") 'port: 8443' "idp.ports must add a separate egress rule"
hasnt <(echo "$np_res") 'cidr: ["'"'"']?0\.0\.0\.0/0' "explicit cidrs must replace the open destinations"
# RBAC-профили. readonly: без secrets и записи (проверено выше для $inc); operator: добавляет операции operate, но не exec и не secrets
has   "$opr" '^kind: ClusterRole$' "operator profile must create a ClusterRole"
has   "$opr" 'deployments/scale' "operator profile must allow scaling"
has   "$opr" '^[[:space:]]*-[[:space:]]+patch[[:space:]]*$' "operator profile must allow patch (restart, cordon)"
hasnt "$opr" '^[[:space:]]*-[[:space:]]+secrets[[:space:]]*$' "operator profile must not grant secrets"
hasnt "$opr" 'pods/(exec|attach|portforward|eviction)' "operator profile must not grant exec, attach, port-forward or eviction"
hasnt "$opr" "^[[:space:]]*-[[:space:]]+['\"]?\\*['\"]?[[:space:]]*$" "operator profile must not use wildcards"
# custom + существующая роль: своей ClusterRole нет, привязка идёт на указанную
hasnt "$cus" '^kind: ClusterRole$' "no ClusterRole expected with rbac.existingClusterRole"
has   "$cus" '^kind: ClusterRoleBinding$' "ClusterRoleBinding missing with rbac.existingClusterRole"
has   "$cus" '^  name: view$' "the binding must reference rbac.existingClusterRole"
render -f "$chart/ci/incluster-values.yaml" --set rbac.allowSecretsRead=true | grep -qE '^[[:space:]]*-[[:space:]]+secrets[[:space:]]*$' \
  || fail "rbac.allowSecretsRead must add the secrets rule"

echo "== negative cases (must fail)"
expect_fail() { # <ожидаемый фрагмент сообщения> <аргументы helm template…>
  local want="$1"; shift
  if out="$(render "$@" 2>&1)"; then fail "render succeeded but must fail: $*"; fi
  grep -q -- "$want" <<<"$out" || fail "error does not mention '$want': $out"
}
expect_fail 'secrets.existingSecret is required'
expect_fail 'secrets.values.secretKey is required' --set secrets.create=true --set secrets.values.databaseUrl=x
expect_fail 'rbac.create requires inCluster.enabled' --set secrets.existingSecret=s --set rbac.create=true
expect_fail 'replicaCount' --set secrets.existingSecret=s --set replicaCount=0
expect_fail 'metrics.port must differ' --set secrets.existingSecret=s --set metrics.enabled=true --set metrics.port=8080
expect_fail 'logLevel' --set secrets.existingSecret=s --set kubron.logLevel=loud
expect_fail 'profile' --set secrets.existingSecret=s --set inCluster.enabled=true --set rbac.create=true --set rbac.profile=admin
expect_fail 'rbac.profile=custom requires' --set secrets.existingSecret=s --set inCluster.enabled=true --set rbac.create=true --set rbac.profile=custom
expect_fail 'requires rbac.profile=custom' --set secrets.existingSecret=s --set inCluster.enabled=true --set rbac.create=true --set rbac.existingClusterRole=view
expect_fail 'clusterAddrPolicy' --set secrets.existingSecret=s --set kubron.clusterAddrPolicy=loose

echo "OK: chart checks passed"
