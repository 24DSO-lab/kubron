# HTTP security

This page describes what the Web server does at the HTTP layer and which settings you control. Settings are environment variables or flags; see the [configuration reference](/server/configuration) for the full list.

[[toc]]

## Security headers

The server sets these headers on every response: pages, static files, the API, `/healthz` and error pages.

| Header | Value |
|---|---|
| `Content-Security-Policy` | `default-src 'self'`; `script-src 'self'` (no `unsafe-inline`, no `unsafe-eval`); `style-src 'self' 'unsafe-inline'`; `img-src 'self' data:`; `font-src 'self'`; `connect-src 'self'` plus `ws://` and `wss://` of its own host; `worker-src 'self' blob:`; `object-src 'none'`; `base-uri 'self'`; `form-action 'self'`; `frame-ancestors 'none'` |
| `Strict-Transport-Security` | `max-age=31536000`, **only** on a secure request (TLS, or `KUBRON_TRUST_PROXY=1` with `X-Forwarded-Proto: https`) and not on loopback. `includeSubDomains` and `preload` are not set: that is the domain owner's decision |
| `Permissions-Policy` | Camera, microphone, geolocation, payment, USB and others are disabled |
| `Cross-Origin-Opener-Policy`, `Cross-Origin-Resource-Policy` | `same-origin` |
| `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy` | `nosniff`, `DENY`, `no-referrer` |

**Why `style-src 'unsafe-inline'`.** The code editor (Monaco) and the terminal (xterm) create `<style>` elements on the fly, and the icon font is attached the same way. Without the exception the editor has no styling. It applies to styles only; scripts stay `'self'`.

**Only the server sets the policy.** There is no `<meta>` CSP tag in the page, because it would also affect the Desktop build.

**Rolling it out.** `KUBRON_CSP_REPORT_ONLY=1` (`--csp-report-only`) sends the policy as `Content-Security-Policy-Report-Only`: violations appear in the browser console and nothing is blocked. Reports are not sent to the server.

## Client address behind a proxy

The audit log and the brute-force lockout need the client address. A client can forge the left part of `X-Forwarded-For`, so the address is taken **from the right**, after the trusted proxies. This works only with `KUBRON_TRUST_PROXY=1`.

| Setting | Meaning |
|---|---|
| `KUBRON_TRUSTED_PROXY_HOPS` (default 1) | How many trusted proxies stand in front of the server, counting the direct peer. The client is the `X-Forwarded-For` element at index `len-hops`. If the chain is shorter than expected (a request that bypassed the proxy), the peer address is used |
| `KUBRON_TRUSTED_PROXIES` | Comma-separated CIDRs or addresses of trusted proxies. When set, it replaces the count: the peer must be in the list (otherwise the header is ignored), and the client is the first address from the right that is outside the list |

Examples: ingress-nginx behind a cloud load balancer, `KUBRON_TRUSTED_PROXY_HOPS=2`; an ingress in a known network, `KUBRON_TRUSTED_PROXIES=10.0.0.0/8`. On any ambiguity (garbage in the header) the direct peer address is used. For IPv6, limits are keyed by the `/64` prefix.

## Server errors (status 500)

On status 500 with code `error`, the client does not receive the raw `err.Error()`: it could contain internal addresses, table names or connection strings. If the error chain contains a database error, a network error (including DNS), a TLS or x509 error, a file path or a timeout, the response contains a generic message `internal error, request id <id>` and a `requestId` field. The same id is in the `X-Request-Id` header and in the server log (`rpc call failed`, field `request_id`), where all the details remain.

Not hidden: Kubernetes API messages, input validation errors and application refusals. Responses with statuses 400, 401, 403, 404 and 409 are unchanged. Errors from connecting to a cluster over the network are generic too; the cause of unreachability is in the log under the `request_id`.

## First administrator (setup token)

The setup call is anonymous: on an empty database it creates an administrator. That is why, in **service mode** (a database, a non-loopback address, or `KUBRON_ALLOW_REMOTE=1`), it requires a token in the `X-Kubron-Setup-Token` header.

- **The token** is set by the operator in `KUBRON_SETUP_TOKEN` (at least 16 characters; environment only, there is no flag). The server does not generate it and does not log it; the value is redacted from logs and never appears in API responses. The web setup screen asks for it when it is required.
- **Without a token** (the variable is not set and there are no users), a network setup call answers `403 setup_disabled`. Create the administrator with `kubron-server create-admin --username <name>`: see the [command reference](/server/cli).
- **A wrong token** gives `403 setup_token_invalid` (the same for missing and wrong), compared in constant time. Five wrong attempts in a row from an address block it (`429`, `Retry-After`), then one attempt per 30 seconds. Correct attempts do not spend the allowance.
- When users already exist, the token is not checked: the core answers that setup is already done. A race between two simultaneous setups, even on different replicas, yields exactly one administrator, because inserting the first user in PostgreSQL is atomic.
- A single-user server on loopback (file storage) and Desktop work as before and need no token.

## Session cookie and the Host header

- **Cookie.** On a secure channel (TLS directly, or `X-Forwarded-Proto: https` from a trusted proxy) the server accepts only `__Host-kubron_session`. A plain `kubron_session` on such a request is ignored, even if it is the only one and valid, and even if a real `__Host-` cookie is present next to it: a neighbouring subdomain or a plain-HTTP connection could plant the plain one (cookie tossing). The plain cookie is accepted only over HTTP and on loopback, where `__Host-` cannot be issued. This also applies to the session endpoint, WebSocket and terminals.
- **`KUBRON_ALLOWED_HOSTS`** is a comma-separated list of accepted `Host` values; empty turns the check off. An entry is a name or IP, optionally with a port: `kubron.example.com` accepts any port, `kubron.example.com:8443` only that one; write IPv6 in brackets (`[::1]:8080`). Case and a trailing dot do not matter, there are no wildcards, and a typo in the list is a startup error. A request with another `Host` gets `421` with an identical response (the list is not revealed) before routing, sessions and RPC. `*.pf.<domain>` streams are allowed automatically when port-forward is on. The `/healthz` and `/readyz` probes are exempt because the orchestrator calls them by the Pod address.

## Rate limiting and lockouts

- **Sign-in and password change.** 5 free attempts per login and 30 per address in 15 minutes, then an exponential lockout of up to 5 minutes. Refusals are written to the audit log with the address (`source`). Guessing the old password through the password-change call counts the same way.
- **Anonymous calls** (no session, including sign-in and setup): a token bucket per address, 20 requests at once and 5 per second. The answer is `429` with `Retry-After` and a body like `{"error": {"code": "rate_limited", "message": …}}`.
- Failure records are kept separately for logins and addresses. When full, stale records are evicted first, then the oldest records **without** a lockout; active lockouts are never dropped.

**Several replicas.**

- **Per-login lockouts are shared.** With PostgreSQL, the counters for failed sign-ins and password changes are in the `login_failures` table. The key is the SHA-256 of the normalized login, including non-existent ones, so a record does not reveal whether a user exists. Brute-forcing across different replicas does not bypass the lockout. If the database is unavailable, the lockout is not checked and an error is logged: a session cannot be opened without the database anyway. The table is capped at 10,000 rows.
- **Per-address counters and the token bucket stay in replica memory.** They are a coarse load limit, not account protection: the effective ceiling is the number of replicas times the threshold.
- File storage (Desktop, single user) keeps all counters in process memory.

## Kubeconfig secrets never leave the server

In service mode (cluster storage in PostgreSQL), the call that returns a cluster's kubeconfig replaces secret values with `<kubron:unchanged>`: `users[].user.token`, `password`, `client-key-data`, `client-key`, secret fields of `auth-provider.config` (`refresh-token`, `client-secret` and so on), `exec.env` values and secret flags in `exec.args`. Certificates (`client-certificate-data`, `certificate-authority-data`) stay, because they are not secrets. YAML comments and layout are kept. If a secret cannot be hidden (unusual layout), the call returns an error instead of text.

- **Saving, testing and inspecting** accept such text: the server substitutes the stored value of the same user in place of the placeholder. A value typed in place of a placeholder replaces the secret.
- **A secret cannot be exfiltrated.** The substitution is refused if the server address, `proxy-url` or the user of a context that uses the user has changed. A placeholder without a stored value, or outside a secret field, is an error too. Otherwise editing the address to a foreign server would make Kubron send the stored token there.
- **Audit.** Replacing, adding or removing a secret is recorded as `cluster.credentials.update`, without the values.
- **Interface.** The edit dialog shows a "Secret hidden" hint and a **Replace secret** button in place of the secret.
- **Desktop and single user.** File storage works as before: the text is returned whole.
