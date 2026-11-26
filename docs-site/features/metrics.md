# Metrics stack and charts

Kubron can draw CPU, memory, network and disk charts for pods, nodes, volumes, ingresses and workloads, and it uses the same data for the dashboard hotspots. The data comes from a small Prometheus stack that Kubron installs into the cluster **when you ask for it**. Everything else in Kubron works without it.

::: warning Before you rely on it
- **It is Kubron's own stack.** Kubron reads only the Prometheus it installs into `kubron-metrics`. It does not connect to a Prometheus you already run, and it is not a replacement for real monitoring.
- **It keeps 2 days of history** (or 5 GiB) on an `emptyDir`, so the history is lost when the Prometheus Pod restarts. The **7 days** option in the charts cannot show more than 2 days of data.
- **Ingress charts work for ingress-nginx controllers only.** Other ingress controllers are not scraped.
:::

[[toc]]

## What you get

| Where | Charts |
|---|---|
| Pod, in the containers tab | CPU, memory, network, disk and disk I/O |
| Node | CPU, memory, network and disk, with capacity and allocatable lines |
| PersistentVolumeClaim | Volume usage and capacity |
| Ingress | Request and response traffic, including bytes sent with 2xx and 5xx status (ingress-nginx controllers only) |
| Deployment, ReplicaSet, StatefulSet, DaemonSet, Job | Usage of all their Pods together |
| Namespace and Argo CD application | Usage of all Pods in the namespace or of the application |
| Cluster dashboard | The three Pods closest to their CPU and memory limits and the three fullest volumes ([details](/features/dashboard#metrics-and-hotspots)) |

Charts offer 1 hour, 6 hours, 24 hours and 7 days. The stack keeps data for **2 days** (see [Limits](#retention-and-limits)), so the 7-day range shows at most what is stored. A chart without data says **No data available**.

## How it works

The stack runs in its own namespace, `kubron-metrics`. Kubron queries Prometheus through the Kubernetes API server (the `services/proxy` path), using the same credentials you use for the cluster. Nothing is published outside the cluster: all three Services are ClusterIP, and there is no Ingress or NodePort.

Kubron reads only **its own** stack (`prometheus` in `kubron-metrics`). It does not connect to a Prometheus you already run.

## Install it

1. Open **Settings** and find **Metrics stack**.
2. Click **Install metrics stack**. Installing is available to administrators.
3. Wait a few minutes. The status moves from **Installing** to **Active** with a counter such as `3/3 ready`.

Kubron applies the manifest with your credentials, so those credentials need Kubernetes rights to create a namespace, ClusterRoles and ClusterRoleBindings. In practice that means a cluster administrator.

### What gets installed

| Component | Kind | Purpose | Resources |
|---|---|---|---|
| `prometheus` v2.55.1 | StatefulSet, 1 replica | Collects and stores metrics | Requests 100m CPU and 512 Mi; no limits |
| `node-exporter` v1.7.0 | DaemonSet, one Pod per Linux node | Hardware and OS metrics | Requests 10m and 24 Mi; limits 200m and 100 Mi |
| `kube-state-metrics` v2.11.0 | Deployment, 1 replica | Metrics about Kubernetes objects | Requests 10m and 32 Mi; limits 200m and 150 Mi |

Prometheus scrapes every 15 seconds: the API server, kubelets and cAdvisor, node-exporter, kube-state-metrics, ingress-nginx controllers (labelled `app.kubernetes.io/name=ingress-nginx`), etcd where it is reachable, and any Pod or Service endpoint annotated `prometheus.io/scrape: "true"`. It also creates the ServiceAccounts, ClusterRoles and bindings it needs, two ConfigMaps (configuration and rules) and a short-lived init container (`alpine`) that fixes the permissions of the data directory.

### Requirements

- **Image registries.** The images come from `quay.io` (Prometheus, node-exporter), `registry.k8s.io` (kube-state-metrics) and `docker.io` (`alpine`). In an air-gapped cluster, mirror them first.
- **Linux nodes.** All three components are scheduled on `kubernetes.io/os=linux` nodes only.
- **Node access.** `node-exporter` runs on every node, including tainted ones (it tolerates `NoSchedule`), and mounts `/proc`, `/sys` and `/` of the host read-only. That is how node-exporter works, but it matters if your policies restrict `hostPath`.

## Who can see the charts

Metrics reveal the names of Pods and namespaces for the whole cluster, so access is **not** granted to everyone. The stack creates the Role `prometheus-proxy-access` in `kubron-metrics`; cluster administrators can already use it, and everyone else needs a RoleBinding. For example, for a group:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubron-metrics-viewers
  namespace: kubron-metrics
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: prometheus-proxy-access
subjects:
  - kind: Group
    name: dev-team
    apiGroup: rbac.authorization.k8s.io
```

In the Web edition, the identity Kubron uses for the cluster (a team's account, or the in-cluster ServiceAccount) needs this access, and Kubron's own roles then decide who may open the charts.

## Retention and limits {#retention-and-limits}

- **2 days or 5 GiB**, whichever is reached first.
- **Storage is an `emptyDir`.** If the Prometheus Pod is restarted or rescheduled, the history is lost and charts start again. Use it for recent troubleshooting, not as long-term monitoring.
- The 7-day range cannot show more than the stored 2 days.
- Scraping of the API server and kubelets does not verify their TLS certificates inside the cluster. This is the usual in-cluster Prometheus setup, but note it if your policy requires verification.

## Status and removal

| Status | Meaning |
|---|---|
| **Not installed** | The namespace does not exist |
| **Installing** | The stack was created less than five minutes ago and is still starting |
| **Active** | All components are ready; the card lists them as `available/total` |
| **Degraded** | A component is missing or not ready after the startup period. Use **Reinstall** to apply the manifest again |

**Remove metrics stack** deletes the `kubron-metrics` namespace **with all its data**, after a second confirmation.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| Charts say **No data available** right after install | Prometheus needs a few scrape intervals (15 s each). Wait a minute and reopen the chart |
| Charts are empty for everyone but the stack is **Active** | Your user lacks access to `services/proxy`. Add the RoleBinding above |
| Ingress charts are empty | The ingress controller must be scraped by this Prometheus (ingress-nginx metrics). Other controllers are not covered |
| History disappeared | The Prometheus Pod was restarted or moved. Data lives in an `emptyDir` |
| Status stays **Installing** or turns **Degraded** | Check the Pods in `kubron-metrics`: image pull errors (registry access), scheduling (Linux nodes, resources) or the init container |
| Install fails with `forbidden` | The credentials cannot create ClusterRoles or a namespace. Use an administrator's credentials |

See also [Cluster dashboard](/features/dashboard) and [Configuration](/server/configuration) for the Web edition.
