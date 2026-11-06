# Connecting to Kubernetes

Kubron detects the credential type from the selected kubeconfig context. You do not need to choose a connection mode manually. When interactive sign-in is required, Kubron opens a challenge dialog and guides you through the next step.

## Credentials supported by the current build

- **Static credentials:** bearer tokens, basic credentials and client certificates embedded in kubeconfig data.
- **OIDC:** supported `oidc-login` flows (password, authorization code and device code) and the legacy `auth-provider: oidc` refresh flow, subject to the identity provider configuration.
- **Azure AKS:** Azure `kubelogin` device-code, service-principal client-secret and username/password (ROPC) modes.
- **AWS EKS:** `aws eks get-token` and `aws-iam-authenticator` kubeconfigs, using access keys, temporary session keys, an optional role (`role-arn`) or IAM Identity Center (device code).
- **Google GKE:** `gke-gcloud-auth-plugin` kubeconfigs, using a service-account key or, if configured, your own Google account.
- **Other exec plugins:** handled according to the platform-specific rules below.

The exact sign-in options depend on the kubeconfig and the identity provider. Unsupported modes are rejected rather than silently replaced with credentials from the server environment.

## Desktop

Desktop connects to the Kubernetes API from your computer.

- Import a kubeconfig file or paste its contents, then select a context if there are several.
- When a native provider supports the configured flow, Kubron asks for the required credentials or opens the browser/device sign-in.
- Kubernetes `exec` credential plugins can run locally when the executable is installed in `PATH`. Before running a plugin, Kubron shows the command, arguments and environment variable names (not their values) for your confirmation. A changed command requires confirmation again.
- If a native provider cannot handle an `exec` mode, Kubron can use the confirmed local plugin when one is configured.

An `exec` plugin is executable code. Import kubeconfigs only from sources you trust, and review the command before approving it.

## Web

The Web server connects to clusters from the server's network environment. It cannot read files from your workstation and **never executes arbitrary commands** from an uploaded kubeconfig.

- Static credentials must be embedded in kubeconfig data. Local file paths are not available to the server.
- Supported OIDC flows use the challenge dialog. Depending on the IdP and its configuration, this can involve entering credentials, device-code sign-in, or completing a browser redirect.
- Azure AKS supports the configured `kubelogin` device-code, service-principal client-secret and ROPC modes. ROPC may be unavailable when the tenant requires MFA or Conditional Access.
- AWS EKS and Google GKE are handled by native providers too: Kubron issues the EKS token itself and signs in to GKE with a service-account key or a Google account. See [AWS EKS](#aws-eks) and [Google GKE](#google-gke).
- For an `exec` plugin without a native provider, Kubron displays the exact command for you to run on your own computer and accepts the resulting token or `ExecCredential` JSON. The server does not run that command.

The server does not use your workstation's Azure CLI, cloud credentials, environment variables, home directory or metadata service. AKS managed identity, Azure CLI, `azd` and workload-identity modes are not supported by this provider.

## AWS EKS

For an EKS cluster (`aws eks get-token` or `aws-iam-authenticator` in the kubeconfig), Kubron issues the token itself and does not run the plugin. The sign-in dialog asks for:

1. **Sign-in method.** Type one word: `keys` (permanent IAM user keys), `session` (temporary keys with a session token) or `sso` (IAM Identity Center). If the kubeconfig does not name the region and cluster, enter them here too.
2. **Credentials.** For `keys` and `session`: the access key ID and secret key (plus the session token for `session`). Keys starting with `ASIA` are rejected without a session token. For `sso`: the start URL, the SSO region, the account ID and the role name.
3. **`sso` only:** complete the device-code sign-in.

If the kubeconfig names a role (`role-arn`), the token is issued for that role after `AssumeRole`.

What to know:

- Keys are saved encrypted in your own user scope, so later connections do not ask again. About every 14 minutes, when a token is issued, Kubron verifies the keys with STS `GetCallerIdentity` (or `AssumeRole` for a role). Revoked or expired keys trigger a new prompt. If AWS is temporarily unreachable, the token is still issued locally from the saved keys.
- For SSO, the role's temporary keys (about an hour) are saved. When they expire Kubron tries to renew them; if that fails, the dialog asks you to sign in again.
- The server's own credentials (environment variables, `~/.aws`, profiles, instance metadata) are **never** used, and `profile` from the kubeconfig is ignored.
- **Disconnect from the cluster** removes the saved keys in Kubron but does **not** revoke them in AWS. Delete or deactivate the key in IAM yourself.
- Not supported: an unknown `authenticator`, isolated AWS partitions (`iso*`) and `--cluster-id` (Outposts). Use a manually obtained token for those.
- IAM Identity Center sign-in has so far been verified only against a test server, not against real AWS.

Give Kubron users keys of dedicated IAM users or roles with minimal permissions. Access to the cluster itself is granted through `aws-auth` or EKS access entries. Rotate keys on a schedule.

## Google GKE

For a GKE cluster (`gke-gcloud-auth-plugin` in the kubeconfig), Kubron signs in itself instead of running the plugin. There are two methods:

1. **Your own Google account**, when an OAuth client is configured. On the Web edition the administrator sets it with `KUBRON_GKE_OAUTH_CLIENT_ID` and `KUBRON_GKE_OAUTH_CLIENT_SECRET` (see [deployment](/server/deploy#optional-browser-sign-in-and-google-accounts)). On Desktop you configure it in **Settings**, in the *Google sign-in for GKE* card. Google's page opens and, afterwards, the browser returns to Kubron. If the return page cannot open (no `KUBRON_PUBLIC_URL` on the server, or the local port could not be opened), copy the full address of that page and paste it into the field. Kubron saves the refresh token encrypted and renews access on its own.
2. **A service-account key** (the JSON file issued by Google Cloud): paste its contents into the field.

If a Google client is configured, new sign-ins use the Google account; a previously saved key keeps working, and a key that stops being accepted is requested again.

On Desktop, create an OAuth client of type *Desktop app* in Google Cloud, enter its ID and secret in the card and restart Kubron. The secret is stored encrypted and never shown again.

What to know:

- The key is exchanged for an access token at the fixed address `https://oauth2.googleapis.com/token`. The `token_uri` in the file is ignored.
- The server's own Google credentials (Application Default Credentials, metadata server, environment variables, gcloud files) are **never** used.
- A saved key is kept encrypted, and the token lasts about an hour. The key file may be at most 16 KiB.
- **Disconnect from the cluster** deletes the saved key in Kubron but does **not** revoke it at Google. Delete the key in Google Cloud. Access granted to a Google account is revoked on the account's *Security* page (third-party access).
- Workload Identity Federation, `authorized_user` and `impersonated_service_account` credentials are not supported. Use a manually obtained token instead.

Create a separate service account per cluster or group of clusters with minimal roles, grant its `client_email` access through Kubernetes RBAC, rotate keys on a schedule and delete unused ones in Google Cloud.

## Whose Kubernetes identity is used?

Static credentials remain shared cluster credentials. Interactive credentials are stored in the signing-in user's scope, so each user can authenticate with their own Kubernetes identity. The effective access is constrained by both Kubron's application permissions and Kubernetes RBAC.

Passwords for personal interactive sign-in are not stored. Where a provider supports refresh tokens, Kubron stores the provider's opaque refresh state encrypted.

## Shared team account

For a cluster that belongs to a team and signs in through an identity provider, a cluster administrator can switch on **Shared team account** in the add or edit dialog. The administrator signs in once when connecting; the provider verifies the sign-in and Kubron stores it encrypted for the whole team. Other members connect with it and, until it exists, see "ask a cluster administrator". If the password changed at the provider, use **Forget the shared sign-in** in the cluster settings. Switching the option on or off forgets the saved sign-ins of that cluster.

## Troubleshooting

- **The sign-in dialog says the mode is unsupported:** check the `users[].user` authentication settings in the selected kubeconfig context. Some cloud modes are intentionally unsupported.
- **Device sign-in does not complete:** make sure the device code is entered in the correct tenant and has not expired.
- **AKS username/password sign-in fails:** ROPC is commonly blocked by MFA or Conditional Access. Use device-code sign-in or another mode enabled by your administrator.
- **EKS asks for keys again:** the saved keys were revoked or expired, or the SSO role keys ran out. Enter them again.
- **GKE says the key is not valid JSON or not supported:** paste the whole service-account file including the braces. Other credential types are not supported.
- **An exec plugin is unavailable in Web:** this is expected. Run the displayed command on your workstation and paste its token or `ExecCredential` result.

[Teams and shared access](/features/teams) · [Security and access control](/features/security)
