#!/usr/bin/env bash
# Copyright 2026 The Kubron Authors
# SPDX-License-Identifier: Apache-2.0
#
# Generate kubeconfigs for visually testing cloud exec-plugin sign-in in Kubron.
# No cluster, cloud resource, or credential is created by this script.

set -euo pipefail

out_dir="${KUBRON_AUTH_LAB_DIR:-.kubron-auth-lab}"
eks_cluster="${EKS_CLUSTER_NAME:-kubron-auth-lab}"
eks_region="${AWS_REGION:-eu-west-1}"
aws_profile="${AWS_PROFILE:-}"
aks_tenant="${AKS_TENANT_ID:-}"

fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
valid_atom() { [[ "$1" =~ ^[A-Za-z0-9._-]+$ ]]; }

valid_atom "$eks_cluster" || fail "EKS_CLUSTER_NAME may contain only letters, digits, dot, underscore and hyphen"
valid_atom "$eks_region" || fail "AWS_REGION may contain only letters, digits, dot, underscore and hyphen"
if [[ -n "$aws_profile" ]]; then
  valid_atom "$aws_profile" || fail "AWS_PROFILE may contain only letters, digits, dot, underscore and hyphen"
fi
if [[ -n "$aks_tenant" && ! "$aks_tenant" =~ ^[A-Fa-f0-9-]{36}$ ]]; then
  fail "AKS_TENANT_ID must be a tenant GUID"
fi

mkdir -p "$out_dir"
cat > "$out_dir/.gitignore" <<'IGNORE'
*
!.gitignore
IGNORE

# The endpoint is intentionally closed. Kubron can request credentials first;
# after successful auth, the expected result is an API connection error.
server='https://127.0.0.1:1'

cat > "$out_dir/eks.kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: kubron-auth-lab-eks
    cluster:
      server: $server
contexts:
  - name: kubron-auth-lab-eks
    context:
      cluster: kubron-auth-lab-eks
      user: kubron-auth-lab-eks
      namespace: default
current-context: kubron-auth-lab-eks
users:
  - name: kubron-auth-lab-eks
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1beta1
        command: aws
        args:
          - eks
          - get-token
          - --cluster-name
          - "$eks_cluster"
          - --region
          - "$eks_region"
        interactiveMode: Never
        provideClusterInfo: false
EOF
if [[ -n "$aws_profile" ]]; then
  cat >> "$out_dir/eks.kubeconfig" <<EOF
        env:
          - name: AWS_PROFILE
            value: "$aws_profile"
EOF
fi

cat > "$out_dir/gke.kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: kubron-auth-lab-gke
    cluster:
      server: $server
contexts:
  - name: kubron-auth-lab-gke
    context:
      cluster: kubron-auth-lab-gke
      user: kubron-auth-lab-gke
      namespace: default
current-context: kubron-auth-lab-gke
users:
  - name: kubron-auth-lab-gke
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1beta1
        command: gke-gcloud-auth-plugin
        args:
          - --use_application_default_credentials
        interactiveMode: Never
        provideClusterInfo: false
EOF

cat > "$out_dir/aks.kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: kubron-auth-lab-aks
    cluster:
      server: $server
contexts:
  - name: kubron-auth-lab-aks
    context:
      cluster: kubron-auth-lab-aks
      user: kubron-auth-lab-aks
      namespace: default
current-context: kubron-auth-lab-aks
users:
  - name: kubron-auth-lab-aks
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1beta1
        command: kubelogin
        args:
          - get-token
          - --login
          - devicecode
          - --server-id
          - 6dae42f8-4368-4678-94ff-3960e28e3630
EOF
if [[ -n "$aks_tenant" ]]; then
  cat >> "$out_dir/aks.kubeconfig" <<EOF
          - --tenant-id
          - "$aks_tenant"
EOF
fi
cat >> "$out_dir/aks.kubeconfig" <<'EOF'
        interactiveMode: Never
        provideClusterInfo: false
EOF

printf 'Created test kubeconfigs in %s\n\n' "$out_dir"
printf '  %s\n' "$out_dir/eks.kubeconfig" "$out_dir/gke.kubeconfig" "$out_dir/aks.kubeconfig"
printf '\nCLI availability (missing binaries can still be used to test Kubron native fallback, if registered):\n'
for bin in aws gke-gcloud-auth-plugin kubelogin; do
  if command -v "$bin" >/dev/null 2>&1; then
    printf '  found: %s (%s)\n' "$bin" "$(command -v "$bin")"
  else
    printf '  missing: %s\n' "$bin"
  fi
done
printf '\nNo credentials were read or stored. The API endpoint is intentionally unavailable.\n'
