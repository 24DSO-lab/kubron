# Day-to-day operations

Kubron brings common Kubernetes troubleshooting and operational actions into the same workspace as resource browsing.

## Workloads

From resource details and workload views you can:

- inspect Pods, containers, conditions, events and ownership;
- read container logs, including a live log stream;
- open an interactive terminal in a container when Kubernetes RBAC and Kubron permissions allow it;
- evict a Pod;
- scale Deployments, StatefulSets and ReplicaSets;
- restart Deployments, StatefulSets and DaemonSets;
- inspect Deployment rollout revisions and roll back to a previous revision;
- retry Jobs, trigger CronJobs and change their suspended state.

Actions that change cluster state are permission-checked by Kubron and Kubernetes.

## Nodes

Inspect node details and perform common maintenance actions:

- cordon a node to stop scheduling new Pods;
- drain a node to evict eligible workloads;
- open a node shell when explicitly permitted.

The node shell is a privileged operation: it uses a privileged helper Pod with access to the host. It has a separate Kubron permission (`nodeshell`); by default, only administrators receive it.

## Logs, terminals and port-forward

- Pod logs can be viewed on demand or followed as a live stream.
- Pod terminals use Kubernetes exec/attach.
- Port-forward can target a Pod or a Service.

Desktop port-forward opens a local address on the same computer as Kubron. In Web service mode, port-forward is disabled unless the operator configures `KUBRON_PF_BASE_DOMAIN`, wildcard DNS and a wildcard TLS certificate. This uses a per-stream proxy origin rather than exposing a listening port on the server host.

Server operators can disable interactive features globally with `KUBRON_DISABLE_EXEC`, `KUBRON_DISABLE_NODE_SHELL` and `KUBRON_DISABLE_PORT_FORWARD`. Per-user limits and session timeouts also apply.

## Metrics

Kubron can optionally install a small metrics stack in the active cluster:

- Prometheus
- kube-state-metrics
- node-exporter

When it is available, Kubron can show resource usage, trends and workload/volume hotspots. Metrics are optional; the core resource browser and cluster overview do not require this stack.

Installing or removing the stack changes cluster resources and requires the appropriate write permission.

## Edition availability

| Capability | Desktop | Web |
|---|---|---|
| Pod logs and terminal | Yes | Yes, subject to server policy |
| Node shell | Yes, subject to permissions | Yes, subject to server policy and permissions |
| Port-forward | Local port | Local single-user mode, or proxy mode configured by the server operator |
| Optional metrics stack | Yes | Yes |

[Explore the cluster dashboard](/features/dashboard) or [review security and permissions](/features/security).