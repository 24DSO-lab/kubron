# Cluster dashboard

The dashboard is a starting point for understanding the active cluster: its health, its capacity and the changes that may explain a recent incident. Most of it is calculated from the Kubernetes API alone, so it works without any extra components in the cluster.

## Cluster health

- **Health score and status.** A score from 0 to 100 with a status of *Healthy*, *Degraded* or *Critical*.
- **Control plane components.** Each component in `kube-system` is shown as healthy or unhealthy. On managed clusters where the control plane is not visible, a component is reported as *unknown* rather than as a failure.
- **Totals.** Ready and total nodes, Pods by phase, Deployments, namespaces and Warning events.
- **Workloads table.** Deployments, StatefulSets and DaemonSets with ready and desired replicas, how many are up to date, and a status of *Running*, *Degraded*, *Unavailable* or *Idle*.
- **Reserved capacity.** CPU and memory requested by Pods compared with what the nodes can allocate.

### How the health score is calculated {#health-score}

The score (0 to 100) is the weighted share of three checks:

| Check | Weight | What counts as healthy |
|---|---|---|
| Control plane components | 40% | Components in `kube-system` that report healthy. A component Kubron cannot see (typical on managed clusters) is left out, and the rest share its weight |
| Nodes | 30% | Nodes that report **Ready** |
| Pods | 30% | Pods whose phase is `Running` or `Succeeded` |

A score of 90 or more is **Healthy**, 70 to 89 is **Degraded**, and below 70 is **Critical**.

The score is a quick signal, not a Kubernetes health score. It does **not** include container readiness, restarts and out-of-memory kills, Warning events, quotas, PodDisruptionBudgets, certificates or the state of workloads. A Pod whose container keeps crashing usually still has the phase `Running`, so the score can stay high while the problem-Pods list shows trouble. Read the sections below for the details.

## Problem Pods and warnings

- **Problem Pods** with the reason, node, restart count and how long the problem has lasted. The list shows up to 10 Pods. To avoid noise, a Pod is not reported while it is still starting (pending for under 5 minutes, or not ready for under 2 minutes), and old failed or evicted Pods (older than 24 hours) are ignored.
- **Restarts and out-of-memory kills** over the last 24 hours.
- **Warning events** from the last hour grouped by reason (the five most frequent), plus the eight most recent events.

## What could block scheduling or maintenance

- **Node headroom:** how much CPU and memory is still free on each node for new requests.
- **Quota pressure:** ResourceQuotas that are used at 80% or more.
- **Blocking PodDisruptionBudgets:** budgets that currently allow no voluntary eviction, which would stall a node drain.

Each of these lists shows up to 10 rows, with a total count.

## What changed recently

The recent-changes timeline combines Deployment rollouts, Helm release updates, Argo CD syncs and Flux reconciles (the time a Kustomization or HelmRelease last changed its Ready condition), newest first (up to 15 entries). Switch the period between 1 hour, 6 hours and 24 hours. A source that is unavailable, for example because Flux is not installed or you lack permission to read it, simply adds no entries. It answers "what changed before this broke". It is built from what Kubron can observe through the Kubernetes API and installed integrations; it is not a complete audit trail of every cluster change.

## Certificates

::: warning Needs access to Secrets
This card works only if you have the Kubron `secrets.read` permission and Kubernetes lets you read Secrets. Without them it cannot show certificates, which does not mean that none is about to expire.
:::

Kubron reads TLS Secrets (`kubernetes.io/tls`) and reports certificates that are expired or expire within 30 days. For certificates issued by cert-manager, which renews them ahead of time, it warns only when 14 days or fewer remain, because that suggests that automatic renewal did not work. Only the name, subject and expiry date are returned, never key material.

This view requires the Kubron `secrets.read` permission and the Kubernetes permission to list Secrets.

## Metrics and hotspots

Everything above works without a metrics stack. The following needs Prometheus data from the optional metrics stack (Prometheus, kube-state-metrics and node-exporter):

- the three Pods closest to their **memory** limit and the three closest to their **CPU** limit (only Pods that have limits are ranked);
- the three most full **PersistentVolumeClaims**, based on kubelet volume statistics for mounted volumes;
- resource usage charts and trends.

Pending and lost PersistentVolumeClaims are counted from the Kubernetes API and are shown even without the metrics stack.

[Learn about the optional metrics stack](/features/metrics) and [operational actions](/features/operations).
