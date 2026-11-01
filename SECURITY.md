# Security Policy

Kubron works with kubeconfig files, cluster credentials and Secret values, so we take
security reports seriously.

## Supported versions

Security fixes are released for the latest published version only.

| Version | Supported |
|---|---|
| Latest release | ✅ |
| Older releases | ❌ |

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately through GitHub:
[Report a vulnerability](https://github.com/24DSO-labs/kubron/security/advisories/new).

Please include:

- affected version and platform;
- steps to reproduce or a proof of concept;
- the impact you expect (for example, access to credentials or privilege escalation).

## Scope

In scope: the Kubron desktop app, the experimental web server (`cmd/kubron-server`),
local authentication and roles, the audit log and the handling of kubeconfig and tokens.

Out of scope: vulnerabilities in third-party dependencies that are already fixed
upstream (Dependabot tracks these), and issues that require an already compromised
machine or user account.
