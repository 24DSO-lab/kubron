# Settings and cluster settings

Kubron has two kinds of settings: a few for the application (language, refresh, metrics) and a dialog for each cluster (name, kubeconfig, default namespace, appearance and more).

[[toc]]

## Application settings

Open **Settings** in the sidebar.

| Setting | What it does |
|---|---|
| **Language** | The interface language: English, Русский or 中文. The choice is remembered on this device, and the first start follows your system language |
| **Auto-refresh** | How often resource lists refresh, in seconds. The default is 10. Lists can always be refreshed by hand |
| **Metrics stack** | Installs, checks and removes the optional Prometheus stack. See [Metrics stack and charts](/features/metrics) |

::: warning Turning auto-refresh off
Entering `0` turns auto-refresh off, and the lists then refresh only by hand. In the current version a saved `0` is replaced by the default 10 seconds the next time Kubron starts.
:::

## Cluster settings

Open the cluster menu in the top bar and choose **Edit**, or choose **Add** for a new cluster. The same dialog is used for both.

| Field | Notes |
|---|---|
| **Name** | Shown in the cluster switcher and the header |
| **Kubeconfig** | Kubron keeps a copy in its own folder. You can **replace it with a file** or **edit the text** (the text contains credentials, so do not share your screen). In the Web edition, secret values in the text are hidden and kept as they are; use **Replace secret** to change one. **Revert** drops your edits |
| **Context** | The kubeconfig context to use. The default one is marked |
| **Default namespace** | Selected automatically when you connect. Lowercase Latin letters, digits and hyphens, at most 63 characters. Leave it empty to start with all namespaces |
| **Test connection** | Reports the Kubernetes version and the latency, or the reason it failed. See below |
| **Environment** | Not set, Dev, Test, Stage or Prod. While a **Prod** cluster is open, a red line is shown in the header so it cannot be mistaken for another cluster |
| **Icon** and **Color** | A monogram from the name or an icon, and a preset or custom color |
| **Argo CD address** | The published address of Argo CD (for example an Ingress): `http` or `https` without credentials, query or fragment. If set, Kubron opens it instead of forwarding a port. If empty, Argo CD opens through the cluster API |
| **Delete cluster** | Removes the cluster and its kubeconfig from Kubron after a confirmation. Your original kubeconfig is not touched |

### Web edition only

| Field | Notes |
|---|---|
| **Team** (owner) | The team that owns the cluster. Its members get access according to their roles. You need to be a team administrator to add a cluster |
| **Add the cluster Kubron runs in** | Uses the server's ServiceAccount, so everyone with a role in the team gets that ServiceAccount's rights. See [Access for the in-cluster ServiceAccount](/server/deploy#access-for-the-in-cluster-serviceaccount) |
| **Shared team account** | On: everyone in the team connects with one account, and a cluster administrator signs in once. Off: each user signs in with their own account |
| **Forget the shared sign-in** | Drops the saved shared sign-in. The next connection asks a cluster administrator to sign in again. Changing the account setting does the same for everyone |

If your access to a cluster is removed, Kubron tells you and closes its terminals, log streams and port-forwards.

### Test connection

| Result | Meaning |
|---|---|
| **Reachable**, with version and latency | The address and the credentials work |
| **Could not connect** with a reason | Address, TLS or credentials failed. See [Troubleshooting](/guide/troubleshooting#connecting-to-a-cluster) |
| Warning: **no permission to read namespaces** | The API server answered, but you cannot list namespaces. Many views still work; choose namespaces yourself in the selector. See [Working with resources](/features/working-with-resources#choosing-namespaces) |
| Warning that the kubeconfig signs in through a plugin or identity provider | The address is reachable. The sign-in itself is checked when you connect |

### Cluster status

The cluster bar shows each cluster's state: **Available**, **Unreachable**, **Credentials rejected**, **Misconfigured** or **Not checked**. In the Web edition the server checks clusters in the background (`KUBRON_CLUSTER_CHECK_INTERVAL`, 30 seconds by default).
