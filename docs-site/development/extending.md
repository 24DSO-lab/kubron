# Extending (pkg/ext)

Kubron exposes a public Go API under `pkg/ext` for external modules (SSO/OIDC, custom storage, etc.).

The open core always includes local users, roles, audit log and emergency admin access. SSO/OIDC for organizations is planned as a commercial add-on.

See the package documentation and examples under `examples/ext-authenticator` in the repository.
