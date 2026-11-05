# Connecting to Kubernetes

Kubron detects the credential type from the selected kubeconfig context. You do not need to choose a connection mode manually. When interactive sign-in is required, Kubron opens a challenge dialog and guides you through the next step.

## Credentials supported by the current build

- **Static credentials:** bearer tokens, basic credentials and client certificates embedded in kubeconfig data.
- **OIDC:** supported `oidc-login` flows (password, authorization code and device code) and the legacy `auth-provider: oidc` refresh flow, subject to the identity provider configuration.
- **Azure AKS:** Azure `kubelogin` device-code, service-principal client-secret and username/password (ROPC) modes.
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
- For an `exec` plugin without a native provider, Kubron displays the exact command for you to run on your own computer and accepts the resulting token or `ExecCredential` JSON. The server does not run that command.

The server does not use your workstation's Azure CLI, cloud credentials, environment variables, home directory or metadata service. AKS managed identity, Azure CLI, `azd` and workload-identity modes are not supported by this provider.

## Whose Kubernetes identity is used?

Static credentials remain shared cluster credentials. Interactive credentials are stored in the signing-in user's scope, so each user can authenticate with their own Kubernetes identity. The effective access is constrained by both Kubron's application permissions and Kubernetes RBAC.

Passwords for personal interactive sign-in are not stored. Where a provider supports refresh tokens, Kubron stores the provider's opaque refresh state encrypted.

## Troubleshooting

- **The sign-in dialog says the mode is unsupported:** check the `users[].user` authentication settings in the selected kubeconfig context. Some cloud modes are intentionally unsupported.
- **Device sign-in does not complete:** make sure the device code is entered in the correct tenant and has not expired.
- **AKS username/password sign-in fails:** ROPC is commonly blocked by MFA or Conditional Access. Use device-code sign-in or another mode enabled by your administrator.
- **An exec plugin is unavailable in Web:** this is expected. Run the displayed command on your workstation and paste its token or `ExecCredential` result.

[Teams and shared access](/features/teams) · [Security and access control](/features/security)
