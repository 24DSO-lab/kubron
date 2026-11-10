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

Scaling, restarting, rolling back, retrying Jobs and triggering or suspending CronJobs need the Kubron `operate` permission. Evicting a Pod needs the stronger `write` permission, and terminals need `exec`. Every action is also checked by Kubernetes RBAC. See the [permission reference](/features/security#permission-reference).

## Nodes

Inspect node details and perform common maintenance actions:

- cordon a node to stop scheduling new Pods (`operate`);
- drain a node to evict eligible workloads (`write`);
- open a node shell when explicitly permitted.

The node shell is a privileged operation: it uses a privileged helper Pod with access to the host. It has a separate Kubron permission (`nodeshell`); by default, only administrators receive it.

## Logs, terminals and port-forward

- Pod logs can be viewed on demand or followed as a live stream.
- Pod terminals use Kubernetes exec/attach.
- Port-forward can target a Pod or a Service.

### Debug containers

The **Debug container** action on a Pod adds an [ephemeral container](https://kubernetes.io/docs/concepts/workloads/pods/ephemeral-containers/) to the running Pod and attaches a terminal to it. Use it when the application image has no shell or debugging tools. You choose an image, a profile and an optional target container whose process namespace the debug container shares.

- **Permissions:** it needs the separate `debug` permission (administrators by default) and `exec`, because the terminal is attached to the container. The cluster account needs `patch` on `pods/ephemeralcontainers`.
- **Profiles:** `general` adds `SYS_PTRACE` so you can trace processes. `restricted` runs as a non-root user without capabilities and fits Pod Security `restricted` namespaces.
- **It stays in the Pod:** Kubernetes cannot remove or change an ephemeral container, so every session leaves a finished `kubron-debug-*` container in the Pod spec until the Pod is deleted. Kubron marks them in the container list. The shell exits when you close the terminal, so you cannot reconnect to the same container; start a new one.
- **Administrators:** `KUBRON_DEBUG_IMAGES` limits the images that can be used, and `KUBRON_DISABLE_DEBUG` turns the feature off. Creating the container and starting each terminal session are recorded in the audit log.

Desktop port-forward opens a local address on the same computer as Kubron. In Web service mode, port-forward is disabled unless the operator configures `KUBRON_PF_BASE_DOMAIN`, wildcard DNS and a wildcard TLS certificate. This uses a per-stream proxy origin rather than exposing a listening port on the server host.

Server operators can disable interactive features globally with `KUBRON_DISABLE_EXEC`, `KUBRON_DISABLE_NODE_SHELL`, `KUBRON_DISABLE_DEBUG` and `KUBRON_DISABLE_PORT_FORWARD`. Per-user limits and session timeouts also apply.

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
| Pod logs | Yes | Yes |
| Pod terminal (exec/attach) | Yes | Yes, subject to server policy |
| Node shell | Yes, subject to permissions | Yes, subject to server policy and permissions |
| Port-forward | Local port | Local single-user mode, or proxy mode configured by the server operator |
| Optional metrics stack | Yes | Yes |

[Explore the cluster dashboard](/features/dashboard) or [review security and permissions](/features/security).