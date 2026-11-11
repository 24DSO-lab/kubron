# Debug containers

Many production images have no shell and no debugging tools. A **debug container** lets you attach a temporary container with tools to a running Pod, without rebuilding the image or restarting anything. It is the Kubernetes feature called [ephemeral containers](https://kubernetes.io/docs/concepts/workloads/pods/ephemeral-containers/), and the same thing `kubectl debug -it` does.

[[toc]]

## Start one

1. Open the context menu of a Pod in the Pod list and choose **Debug container**.
2. Choose the **Image**. Kubron suggests `docker.io/library/busybox:stable` (a tiny shell and basic tools) and `docker.io/nicolaka/netshoot:latest` (network tools such as `curl`, `dig`, `tcpdump`). You can type another `registry/image:tag` unless your administrator restricted the images.
3. Choose the **Profile**.
4. Optionally choose a **Target container**. The debug container then shares that container's process namespace, so you can see and trace its processes. With **None**, it uses the Pod's own namespaces.
5. Choose **Start**. Kubron adds the container, waits up to 60 seconds for it to run, and opens a terminal attached to it. When it fails early, for example when the image cannot be pulled, Kubron says so at once.

The terminal runs the image's default shell. Closing the terminal ends the shell and the container exits.

## Profiles

| Profile | What it does | Use it when |
|---|---|---|
| **General** (default) | Adds the `SYS_PTRACE` capability, so you can trace the target container's processes with tools such as `strace` | The namespace allows it |
| **Restricted** | Runs as a non-root user (UID 65534, `nobody`), blocks privilege escalation, drops all capabilities and uses the runtime's default seccomp profile | The namespace enforces Pod Security `restricted` |

## What you need {#what-you-need}

| Requirement | Details |
|---|---|
| Kubernetes | 1.25 or newer, where ephemeral containers are generally available |
| Kubron permission `debug` | A separate permission, given to administrators by default. It exists apart from `exec` on purpose: the container starts from any image you choose, runs in the Pod's namespaces, and stays in the Pod spec until the Pod is deleted |
| Kubron permission `exec` | The terminal is attached to the container, so `exec` is needed as well |
| Cluster RBAC | The cluster account needs `patch` on `pods/ephemeralcontainers`. Kubron's in-cluster RBAC profiles do not include it. Add it with `rbac.extraRules` or a custom profile |

```yaml
rbac:
  extraRules:
    - apiGroups: [""]
      resources: [pods/ephemeralcontainers]
      verbs: [patch]
```

The terminal is attached through `pods/attach`, so the cluster account needs that right as well. See [Access for the in-cluster ServiceAccount](/server/deploy#access-for-the-in-cluster-serviceaccount).

## What stays behind

Kubernetes cannot remove or change an ephemeral container. Every debug session leaves a finished container named `kubron-debug-xxxxx` in the Pod spec until the Pod is deleted. Kubron marks such containers with a **debug** label in the container list. You cannot reconnect to the same container, so start a new one next time.

A debug container still runs in the Pod's network and, with a target, in its process namespace. Treat it like a terminal: it can read what the Pod can read.

## For administrators

| Control | Effect |
|---|---|
| `KUBRON_DISABLE_DEBUG=1` (`kubron.disableDebug` in the Helm chart) | Turns the feature off for everyone |
| `KUBRON_DEBUG_IMAGES` (`kubron.debugImages`) | A comma-separated list of the images that may be used. The dialog then says that only images allowed by the administrator can be used. Empty allows any image |
| `KUBRON_DISABLE_EXEC=1` | Turns pod terminals, the node shell **and** debug containers off |
| The `debug` permission | Give it only to people who may also open terminals |

Creating the container and every terminal session on it are recorded in the [audit log](/features/administration#audit-log), including the namespace, Pod, target container, image and profile. See the [configuration reference](/server/configuration).

## Messages

| Message | Cause and fix |
|---|---|
| **debug containers are disabled by the administrator** | `KUBRON_DISABLE_DEBUG` or `KUBRON_DISABLE_EXEC` is set |
| **the cluster account is not allowed to add ephemeral containers to pod ... (RBAC: patch on pods/ephemeralcontainers)** | Add the RBAC rule above |
| **pod ... was not found (ephemeral containers need Kubernetes 1.25 or newer)** | The Pod is gone, or the cluster is too old |
| **the cluster rejected the debug container (image, profile or Pod Security policy)** | Try the **Restricted** profile, another image, or ask for a namespace exemption |
| **pod ... has already finished: a debug container cannot be added** | A completed or failed Pod cannot take new containers |
| **the debug container failed to start** / **exited before it was attached** | The image has no usable default shell, or it cannot run as the chosen user. Try `busybox` |
| **timed out waiting for the debug container ... to start; it stays in the pod spec and cannot be removed** | The image pull is slow or blocked. Check registry access from the node |
| An image that is not allowed is refused | The administrator restricted the images with `KUBRON_DEBUG_IMAGES` |
