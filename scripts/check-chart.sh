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
    kubeconform -strict -summary -kubernetes-version "$k8s_version" "$tmp/$name.yaml"
  else
    echo "kubeconform not found: schema validation of $name skipped"
  fi
done

# Смысловые проверки: не только «рендерится», но и «рендерится как задумано»
min="$tmp/minimal-values.yaml"
ing="$tmp/ingress-values.yaml"
inc="$tmp/incluster-values.yaml"
rep="$tmp/replicas-values.yaml"
dev="$tmp/dev-secret-values.yaml"
has() { grep -qE -- "$2" "$1" || fail "$3"; }
hasnt() { ! grep -qE -- "$2" "$1" || fail "$3"; }

echo "== semantic checks"
# PodDisruptionBudget только при replicaCount > 1
hasnt "$min" '^kind: PodDisruptionBudget' "PDB rendered with replicaCount=1"
has   "$rep" '^kind: PodDisruptionBudget' "PDB missing with replicaCount=2"
# Ingress только по флагу; TLS ingress включает доверие к прокси автоматически
hasnt "$min" '^kind: Ingress$' "Ingress rendered by default"
has   "$ing" '^kind: Ingress$' "Ingress missing when enabled"
has   "$min" 'KUBRON_TRUST_PROXY: "0"' "trustProxy must be off without ingress TLS"
has   "$ing" 'KUBRON_TRUST_PROXY: "1"' "trustProxy must be on with ingress TLS"
render -f "$chart/ci/ingress-values.yaml" --set kubron.trustProxy=false | grep -q 'KUBRON_TRUST_PROXY: "0"' \
  || fail "explicit kubron.trustProxy=false must win over auto"
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

echo "OK: chart checks passed"
