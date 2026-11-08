# Extending (pkg/ext)

Kubron exposes a public Go API in `pkg/ext` for modules that live outside the repository. It exists because code under `internal/` cannot be imported by another Go module, so anything an external module must implement or consume is described in `pkg/ext`, using only the standard library.

::: warning Unstable API
The contracts are marked **unstable** until the SSO stage. `ext.APIVersion` (currently `1`) increases on every incompatible change, and `Registry.Install` refuses to install a module built for a different version. Pin the version of Kubron you build against.
:::

## What is shipped today

All of Kubron, including local users, roles, the audit log, emergency administrator access and the SSO/OIDC work, is Apache-2.0 in this repository (ADR-0013). SSO is not generally available yet: both shells report `SSO: false`, and organization SSO login is still on the roadmap. The contracts exist so that sign-in and storage can be extended or replaced without changing the core.

The server does not yet load external modules at run time. Today the contracts are implemented inside the repository: the PostgreSQL store implements the storage interfaces, and the Wails and web shells implement `Platform`. The `Registry` and the example module exist so the public contract is proven to compile and work as a separate module.

## The contracts

| Area | Types | Purpose |
|---|---|---|
| Modules | `Module`, `Registry` | A module reports its `Name()` and `APIVersion()` and registers its implementations in `Register(*Registry)`. `Registry.Install` checks the version. The registry is safe for concurrent use |
| Sign-in | `Authenticator`, `PasswordAuthenticator`, `RedirectAuthenticator`, `Identity` | A way to sign in to Kubron. `KindPassword` is a login form inside Kubron (local users, LDAP). `KindRedirect` is a browser redirect to an external provider (OIDC, SAML) with `BeginLogin` and `CompleteLogin` |
| Storage | `UserBackend`, `AuditBackend`, `TokenStore`, `UserRecord`, `SecuritySettings` | Persistent users and settings, audit entries, and Argo CD API tokens per cluster |
| Shell | `Platform`, `Capabilities` | What the core needs from its shell: emit events, pick a file, open a URL, report capabilities |

Design rules to keep in mind:

- **The core assigns roles.** An authenticator returns a verified `Identity` (subject, username, display name, email, groups, claims). Kubron's own rules map it to a role, so an authenticator never grants permissions itself.
- **Redirect sign-in is stateless in the module.** Everything a redirect authenticator needs between the two steps travels in `RedirectStart.State` (state, nonce, PKCE verifier). The core stores it on the server, in the session or database, so it works with several replicas.
- **Identifiers are short and unique.** An `Authenticator.ID()` such as `local` or `oidc` appears in URLs and configuration, and registering a duplicate fails.

## A minimal module

`examples/ext-authenticator` in the repository is a complete, tested module. It registers a password authenticator with a static user list, for illustration only:

```go
type Module struct{ users map[string]string }

func (m *Module) Name() string    { return "example-static" }
func (m *Module) APIVersion() int { return ext.APIVersion }

func (m *Module) Register(r *ext.Registry) error {
    return r.RegisterAuthenticator(&static{users: m.users})
}
```

An application assembles modules with `registry.Install(example.New(users))`.

## Contributing instead

If your change belongs in Kubron itself, for example another sign-in method, open an issue first and see [Contributing](/development/contributing). Because `pkg/ext` is a public contract, discuss any change to it in an issue first.
