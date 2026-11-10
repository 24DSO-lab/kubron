# Troubleshooting and FAQ

Find your symptom, read the likely cause, and follow the link when there is a longer explanation. The messages below are the ones Kubron shows, so you can search this page for the text you see.

[[toc]]

## Starting Kubron

| Symptom | What to do |
|---|---|
| Windows says **Windows protected your PC** | Builds are not code-signed yet. Choose **More info, Run anyway**. See [Desktop](/guide/desktop#troubleshooting) |
| macOS says the app is damaged or from an unidentified developer | Remove the quarantine flag or use right-click **Open**. See [Desktop](/guide/desktop#troubleshooting) |
| Linux: missing `libwebkit2gtk-4.1` or `libgtk-3` | Install GTK 3 and WebKitGTK 4.1. See [Desktop](/guide/desktop#troubleshooting) |
| The window is blank on Linux | Try `WEBKIT_DISABLE_DMABUF_RENDERER=1 kubron`. See [Desktop](/guide/desktop#troubleshooting) |
| **Sign-in unavailable**: the users file could not be read | Kubron could not read `~/.kubron/users.json` and stays closed to protect your data. Restore the file from a backup |

## Signing in to Kubron

| Message or symptom | Cause and fix |
|---|---|
| **login temporarily blocked after failed attempts** | Too many wrong passwords. A login gets 5 free attempts and an address gets 30 in 15 minutes, then a lockout of up to 5 minutes grows with each failure. Wait and try again. An administrator can see the refusals in the audit log. See [HTTP security](/server/http-security#rate-limiting-and-lockouts) |
| **initial setup required: create an administrator** | There are no users yet. Create the first administrator on the welcome screen |
| **initial setup is already complete** | An administrator already exists. Sign in instead |
| **the setup token is missing or invalid** (Web) | Enter the value of `KUBRON_SETUP_TOKEN`, or create the administrator with `kubron-server create-admin`. See [Web quick start](/server/quick-start#b-multi-user-trial-with-docker-compose) |
| **initial setup over the network is disabled** (Web) | `KUBRON_SETUP_TOKEN` is not set. Set it, or run `kubron-server create-admin` on the server. See [Command reference](/server/cli#create-admin) |
| **the setup token is too short** (Web) | `KUBRON_SETUP_TOKEN` must have at least 16 characters |
| You are signed out after a while | Idle auto-logout (15 minutes by default; an administrator can change it or turn it off). Web sessions also end after `KUBRON_SESSION_TTL` (12 hours by default) |
| **You have been given a temporary password** | An administrator reset your password. Set your own to continue |
| **invalid or missing CSRF token** (Web) | Reload the page. If it keeps happening behind a proxy, the proxy must pass the original `Host` header: Kubron accepts a request only when the browser's `Origin` host matches `Host` |

## Connecting to a cluster

| Message or symptom | Cause and fix |
|---|---|
| The connection test times out | The API server is not reachable from where Kubron runs: VPN, firewall or proxy. On Web this means reachable **from the server** |
| `x509: certificate signed by unknown authority` | The kubeconfig has no cluster CA, or a corporate proxy re-signs TLS |
| `Unauthorized` | The token or certificate expired or was revoked. Sign in again or replace the credentials |
| `Forbidden` | The user lacks Kubernetes RBAC rights. Kubron permissions never add rights beyond RBAC |
| **no permission to read namespaces: some data may be unavailable** | You cannot list namespaces cluster-wide. Views still work in namespaces you select: pick them in the selector, or set a default namespace in the cluster settings. Namespaces Kubron cannot read are marked **No access**. See [Working with resources](/features/working-with-resources#choosing-namespaces) |
| **the kubeconfig refers to files on the server: embed certificates and tokens in it (the -data fields)** (Web) | The server cannot read files from your computer. Embed `certificate-authority-data`, `client-certificate-data` and `client-key-data`, or use the sign-in dialog |
| Adding a cluster fails because of its address (Web) | The address policy blocks loopback, link-local and cloud-metadata addresses (and private networks under `strict`). See [Configuration](/server/configuration) |

## Cluster sign-in dialog

| Message | Cause and fix |
|---|---|
| **the plugin command was not found; install it and try again** | The `exec` plugin (`aws`, `kubelogin`, ...) is not on the `PATH` the app sees. Start Kubron from a terminal, or use the full path in the kubeconfig |
| **the plugin command is a relative path** | Use an absolute path or a name found in `PATH` |
| **the plugin was not confirmed** | You declined the confirmation. Desktop asks before running any plugin Kubron does not handle itself |
| **the plugin did not finish in time** / **exited with an error** | Run the displayed command in a terminal to see why it fails |
| **plugins that require an interactive terminal are not supported yet** | Run the plugin yourself and paste the token. See [Connecting to Kubernetes](/features/cluster-auth) |
| **the pasted ExecCredential has no token** / **is not a valid ExecCredential** | On Web, paste the JSON that `kubectl`-style plugins print, or the bare token on a single line |
| **client certificates returned by a plugin are not supported** | Embed the client certificate in the kubeconfig instead |
| **the identity provider is unreachable** | The provider's address is not reachable from the server, or the egress policy blocks it. See [Docker and Kubernetes deployment](/server/deploy#optional-browser-sign-in-and-google-accounts) |
| **skipping TLS verification is not allowed here** (Web) | Web sign-in rejects `insecure-skip-tls-verify`. Provide the provider CA as embedded data |
| **the sign-in response came from an unexpected issuer** / **does not match the request** | The browser returned from a different provider or an old attempt. Start the sign-in again and complete it in one go |
| **the identity provider does not offer the device sign-in** | Use the browser method, or enable the device grant on the client |
| **Azure requires an authentication method unsupported by password sign-in** | The account needs MFA. Use device code instead of username and password |
| **the clock of this server differs too much from the AWS clock** (EKS) | Fix time synchronization (NTP) on the Kubron server |
| **AWS does not allow these credentials to assume the role** (EKS) | Check the role's trust policy and the `role-arn` in the kubeconfig |
| **Google sign-in was denied or cancelled** (GKE) | Start again and approve the request on Google's page |
| **the shared account of this cluster is not signed in: ask a cluster administrator** | A team shared account exists but nobody has signed it in yet. A team administrator completes the sign-in once |

## Working with resources

| Message or symptom | Cause and fix |
|---|---|
| **insufficient permissions for this action** | Your Kubron role lacks the permission. See the [permission reference](/features/security#permission-reference) |
| **eviction is blocked by a PodDisruptionBudget** | The budget allows no voluntary eviction right now. Scale up, fix the unhealthy Pods, or wait. The dashboard lists blocking budgets |
| Secret values or certificates are missing | The `user` role has no `secrets.read`. In the in-cluster mode the ServiceAccount also needs `rbac.allowSecretsRead=true`. See [Access for the in-cluster ServiceAccount](/server/deploy#access-for-the-in-cluster-serviceaccount) |
| Dashboard shows no CPU, memory or volume hotspots | They need the optional metrics stack. See [Cluster dashboard](/features/dashboard#metrics-and-hotspots) |
| **chart ... has no versions (semver tags) or access is denied** | The OCI registry has no semver tags for that chart, or it needs credentials. Add the registry with credentials in Helm settings |
| **port-forward is disabled** / **too many port-forwards (maximum N)** | An administrator disabled it (`KUBRON_DISABLE_PORT_FORWARD`) or the per-user limit is reached. See [Configuration](/server/configuration) |
| **timed out establishing port-forward** | The Pod or Service is not reachable or has no ready endpoint |
| Port-forward does not work in Web | Web needs `KUBRON_PF_BASE_DOMAIN` with a wildcard DNS record and certificate for `*.pf.<domain>`. Without it port-forward is off in service mode |
| Terminals or live updates drop after about a minute (Web) | The proxy closes idle WebSockets. Raise the read and send timeouts. See [Docker and Kubernetes deployment](/server/deploy#troubleshooting) |

## Flux CD

| Message or symptom | Cause and fix |
|---|---|
| **This Flux version is not supported** | Only older API versions are served. Update Flux to 2.3 or newer and run `flux migrate` to move stored objects to stable API versions. See [GitOps](/features/gitops#flux-cd) |
| The Flux section is missing although Flux is installed | Kubron treats Flux as not installed when its CRDs cannot be listed (no `list` permission) or when neither the Kustomize nor the Helm controller is present, and it hides the section without an error. The check repeats about every 30 seconds. See [Rights for Flux CD](/server/deploy#rights-for-flux-cd) |
| Some Flux tabs (sources, notifications, image automation) are missing | Flux was installed partially: a tab is shown only when its controller exists |
| Reconcile, suspend, resume or delete fails with `forbidden` | Kubernetes RBAC lacks `patch` or `delete` on those Flux kinds. Kubron permissions (`operate`, `write`) do not override RBAC. Add the rules from [Rights for Flux CD](/server/deploy#rights-for-flux-cd) |
| No action buttons on an object | Notification objects (`Alert`, `Provider`, `Receiver`) and `ImagePolicy` are read-only, and your role needs `operate` for reconcile or `write` for suspend, resume and delete |
| The resources tab of a HelmRelease is unavailable | Flux records the inventory only since 2.8. Older versions have no data to show |
| A resource is marked **remote** | The object uses `spec.kubeConfig`, so its resources are in another cluster and are not resolved locally |
| An object becomes suspended or active again by itself | `spec.suspend` is set in Git, and the parent Kustomization restores it. Change it in Git |
| A deleted object came back | It is defined in Git and the parent Kustomization created it again. Remove it from Git |

## Running the Web server

| Message or symptom | Cause and fix |
|---|---|
| `... is not loopback: the server does not terminate TLS ...` and exit | Set `KUBRON_ALLOW_REMOTE=1` and put a TLS proxy in front. See [Web quick start](/server/quick-start#troubleshooting) |
| **KUBRON_SECRET_KEY is required when a database is used** | Set the key whenever `KUBRON_DATABASE_URL` is set |
| The server will not start and names an unknown or failing key | The key does not match the stored data. See [Backup, restore and key rotation](/server/backup#restore) |
| The site answers `421` | The `Host` is not in `KUBRON_ALLOWED_HOSTS`. See [HTTP security](/server/http-security#session-cookie-and-the-host-header) |
| You cannot stay signed in behind HTTPS | TLS must end at a trusted proxy, and `KUBRON_TRUST_PROXY=1` must be set so the secure cookie is accepted |
| Pods crash with `ImagePullBackOff` or `InvalidImageName` | Use a lowercase image name (`ghcr.io/24dso-lab/kubron`) and a published tag. See [Docker and Kubernetes deployment](/server/deploy#troubleshooting) |
| A replica never becomes ready after an upgrade | Check its log: a missing or wrong key, an unreachable database or a failed migration stops startup on purpose. See [High availability](/server/ha) |
| The audit log or sessions look empty after a restore | Web sessions are not restored on purpose, so users sign in again. Check the audit log retention (`KUBRON_AUDIT_RETENTION`) |

## FAQ

**Do I need `kubectl`?** No. Kubron talks to the Kubernetes API itself. You only need `kubectl`-style plugins (such as `aws` or `kubelogin`) when a kubeconfig uses an `exec` plugin that Kubron does not handle itself.

**Does Kubron send data anywhere?** No telemetry. Desktop connects only to your clusters (and to identity providers you sign in with). The Web edition connects from your server.

**Where is my data?** Desktop and single-user server: `~/.kubron` (`%USERPROFILE%\.kubron` on Windows). Web with PostgreSQL: in the database, with secrets encrypted by `KUBRON_SECRET_KEY`. See [Backup, restore and key rotation](/server/backup).

**Does Kubron change my kubeconfig?** No. It stores a copy of the contents in its own folder. Your original `~/.kube/config` is not modified.

**Can I use several clusters and contexts?** Yes. Add each context as its own cluster with a clear name and environment tag. See [First cluster](/guide/first-cluster).

**Can I use it with limited access?** Yes. You can work in the namespaces you select without cluster-wide `list`. See [Working with resources](/features/working-with-resources#choosing-namespaces).

**What does it cost, and under which license?** Kubron is open source under Apache-2.0, including the Web edition.

**How do I check that a download is genuine?** See [Requirements, download and verification](/guide/installation#verify-your-download).

**How do I report a bug or ask for help?** Open an issue on [GitHub](https://github.com/24DSO-lab/kubron/issues/new/choose) with your OS, the Kubron version and what you tried. For vulnerabilities, follow the [security policy](https://github.com/24DSO-lab/kubron/blob/master/SECURITY.md).
