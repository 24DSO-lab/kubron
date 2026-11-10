# First cluster

After you [install Kubron](/guide/desktop) and create your local administrator, the app is empty until you add a cluster.

## Before you start

You need a way to reach the cluster:

- a **kubeconfig** file. The default one is `~/.kube/config` (on Windows `%USERPROFILE%\.kube\config`), or whatever your `KUBECONFIG` variable points to;
- network access from your computer to the cluster's API server (VPN, bastion rules and so on are up to you);
- if the kubeconfig uses an `exec` credential plugin that Kubron does not handle itself, that command installed and on your `PATH`. Kubron signs in to Azure AKS, AWS EKS and Google GKE on its own; for any other plugin it shows the command and asks you to confirm before running it. See [Connecting to Kubernetes](/features/cluster-auth).

Check that it works with any tool you have. If `kubectl get nodes` works, Kubron will too. Kubron itself does not need `kubectl`.

## Add the cluster

1. Open **Settings → Clusters** or the cluster switcher and choose **Add cluster**.
2. Give it a **name**, for example `Production EU`.
3. Under **Connection**, choose the **kubeconfig source**:
   - **File**: pick your kubeconfig file;
   - **Paste text**: paste the contents of a kubeconfig.
4. Under **Appearance**, set an optional **color**, **icon** and **environment tag** (`dev`, `test`, `stage`, `prod`). These stay visible everywhere, which makes it much harder to run a command against production by accident.
5. *(Optional)* If the cluster runs Argo CD behind an Ingress, enter its published address so Kubron opens it directly instead of forwarding a port.
6. Click **Add cluster**.

Kubron copies the kubeconfig contents into its own folder (your original `~/.kube/config` is not changed), readable only by you. If you later change the original file, use the cluster settings to replace the stored copy.

::: tip Several contexts in one file
If your kubeconfig contains several contexts, add the cluster once per context and give each a clear name and tag.
:::

## Limited access? It still works {#limited-access}

Kubron does not require cluster-wide `list` permission to work in a namespace. Select the namespaces you may read in the [namespace selector](/features/working-with-resources#choosing-namespaces), or set a default namespace in the [cluster settings](/features/settings#cluster-settings). Kubron reads the selected namespaces one by one, shows what you are allowed to see, and marks the ones it could not read with **No access**, rather than showing an empty screen. The connection test warns you when you cannot list namespaces at all.

## What you get

Kubron uses shared informers, so lists and the dashboard stay live without hammering the API server. Open the [dashboard](/features/dashboard) for health, problem pods and recent changes, or browse [resources](/features/resources).

## Multiple clusters

Keep as many clusters as you like and switch between them from a single window. Each cluster keeps its own namespace filter and view state.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Connection test times out | The API server is not reachable from your computer: VPN, firewall or proxy |
| `x509: certificate signed by unknown authority` | The kubeconfig lacks the cluster CA, or a corporate proxy re-signs TLS |
| `Unauthorized` or `Forbidden` | Expired token or missing RBAC permissions for your user |
| A sign-in dialog appears | The cluster needs interactive sign-in (OIDC, AKS, EKS, GKE or an `exec` plugin). Follow the dialog; see [Connecting to Kubernetes](/features/cluster-auth) |
| An `exec` plugin fails | The plugin command is not installed or not on the `PATH` seen by the app (see [Desktop edition](/guide/desktop#troubleshooting)) |

Using the Web edition? The kubeconfig rules differ: see [Connecting to Kubernetes](/features/cluster-auth#web) and the [Web quick start](/server/quick-start#add-your-first-cluster).
