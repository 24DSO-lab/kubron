# Connecting to Kubernetes

Kubron reads kubeconfig contexts and uses the credentials associated with the selected context. You do not need to choose a connection mode manually.

## Desktop

Desktop runs on your computer and connects to the Kubernetes API from that computer.

- Import a kubeconfig file or paste its contents.
- Select the context to use when a kubeconfig contains several contexts.
- Static credentials such as tokens and client certificates are supported.
- Kubernetes exec credential plugins can run locally, using the plugin installed on your computer.

Because an exec plugin is executable code, only import kubeconfigs from sources you trust.

## Web

The Web server connects to clusters from the server's network environment. It cannot read files from your workstation and does not execute arbitrary commands supplied in an uploaded kubeconfig.

Current Web connection paths are:

- static credentials embedded in kubeconfig data, such as a token or client certificate;
- the supported `oidc-login` password-grant flow, where the user supplies the required credentials;
- the server's own in-cluster ServiceAccount, when Kubron is deployed inside Kubernetes and the service-mode team setup is available.

Web rejects kubeconfigs that depend on local file paths, arbitrary `exec` commands or browser-based `oidc-login` flows that the current Web connection flow cannot complete. Do not upload a kubeconfig that relies on a plugin installed only on your laptop and expect the server to run it.

> **Scope note:** the credential-provider architecture describes broader OIDC and cloud-provider flows, but that work is not yet wired into the current application connection RPC and UI. The list above describes the implemented connection paths, not the full future provider design.

## Two layers of permissions

In Web team mode, access is the intersection of:

1. Kubron's application permissions, assigned through the user's role in the team that owns the cluster;
2. Kubernetes RBAC for the credentials used to connect.

For interactive per-user authentication, the planned provider model is intended to preserve each user's Kubernetes identity. Until that flow is integrated, do not assume every Web connection uses an individual Kubernetes identity.

[Learn about teams and shared access](/features/teams) · [Security and access control](/features/security)