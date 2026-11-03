# Web edition

The Web edition runs Kubron as a self-hosted server and makes the interface available in a browser. It is intended for teams and shared environments that need multiple user accounts, roles, permissions and an audit log.

**Hosting model:** Kubron does not provide a hosted SaaS service. Your team deploys and operates the server in its own infrastructure.

## Choose how to run it

### Try it locally

For a local evaluation, the server can use file-based storage and listen only on loopback:

[Follow the local Web quick start](/server/quick-start).

This path is for evaluation, not production deployment.

### Deploy for a team

For a shared environment, deploy the published container image or Helm chart. A production deployment requires PostgreSQL, a persistent encryption key, TLS termination at a trusted reverse proxy or ingress, and appropriate network restrictions.

[Follow the Docker and Kubernetes deployment guide](/server/deploy).

## What is different from Desktop?

- Users access Kubron through a browser instead of launching a local desktop application.
- The server manages user accounts, roles, permissions, sessions and audit history.
- PostgreSQL stores shared application state; kubeconfigs and GitOps credentials are encrypted at rest.
- The server can run multiple replicas when PostgreSQL is configured.

Review the [Web edition overview](/server/) and [security requirements](/features/security) before exposing the service to a network.

## Connect a cluster

After signing in, add a cluster through the Kubron interface. The available operations are governed by the permissions assigned to the user and the server's Kubernetes RBAC configuration.

## Continue exploring

- [Product capabilities](/features/)
- [Configuration reference](/server/configuration)
- [Choose another edition](/guide/getting-started)
