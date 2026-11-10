# Users and audit log

Two screens help administrators keep Kubron under control: **Users**, where accounts and roles are managed, and **Audit log**, where every sensitive action is recorded. Both appear in the sidebar only for people who have the matching permission (see the [permission reference](/features/security#permission-reference)).

[[toc]]

## Users

The **Users** screen needs the `users.manage` permission, which the `admin` role has. It lists local accounts with their login, role, status and last sign-in. Desktop and Web use the same screen: on a shared computer, a Desktop administrator can also add more local accounts.

### Add a user

1. Choose **Add user**.
2. Enter the **login**: 3 to 32 characters, Latin letters, digits, `.`, `-` and `_`.
3. Enter a **temporary password** of at least 8 characters and choose the **role** (`user`, `operator` or `admin`).
4. Choose **Create**. The user sets their own password at first sign-in.

### Manage an account

| Action | What it does |
|---|---|
| Change the role | Takes effect for the user's next actions. You cannot change your own role |
| **Reset password** | Sets a new temporary password that the user must change at the next sign-in. In the Web edition it also ends the user's open sessions |
| **Disable** and **Enable** | A disabled account cannot sign in, and the Web edition rejects its existing sessions. Nothing is deleted |
| **Delete** | Removes the account after a confirmation. It cannot be undone |

Your own password is changed from the user menu, not from this screen.

The **Status** column shows **Active**, **Disabled**, or **Temporary password** when the user has not yet replaced the password they were given.

### Auto sign-out

Kubron signs everyone out after a period of inactivity (no mouse movement or key presses). The setting applies to **all** accounts, and open terminals and port-forwards are closed with the session. Choose **Off**, 5, 10, 15, 30 minutes, 1 hour or 2 hours. The default is 15 minutes.

### Web OIDC client

In the Web edition the Users screen also holds the optional **Web OIDC client** used for browser-based cluster sign-in: a public client ID and the issuer URL. Both are required, and the issuer must be `https`. See [Docker and Kubernetes deployment](/server/deploy#optional-browser-sign-in-and-google-accounts).

## Audit log

Every sign-in, change and sensitive view is written to the audit log with the time, the user and role, the action, the result, details, the source address and, in the Web edition, the team and cluster.

### Who can read it

- The **Audit log** entry is shown to people with the `audit.read` permission (the `admin` role), and they see all records.
- The server also scopes the log for team administrators, that is, for people who hold `clusters.manage` inside a team: they can read only the events of their own teams. The limit is applied by the server and cannot be widened from the browser.

Opening the log and exporting it are recorded too.

### Read and filter

The log lists records from newest to oldest. Filter by **user**, **action**, **object** (namespace or name), **result**, **team** and **cluster**, and choose a **period**: last hour, 24 hours, 7 days, 30 days, or all time. Choose how many records to show: 200, 500 or 1,000. When more records match, a notice says so: narrow the filter or the period to reach the rest.

| Result | Meaning |
|---|---|
| **Done** | The action succeeded |
| **Failed** | The action was attempted and failed |
| **Denied** | Kubron or Kubernetes refused it because of permissions |

### Export

Export the filtered records as **CSV** or **JSON**, up to 10,000 records. If more match, the export contains the latest ones and says so; narrow the filter to get the rest.

### What is recorded

| Area | Examples |
|---|---|
| Accounts and sessions | Initial setup, sign-in, sign-out, auto sign-out, password change and reset, user creation, deletion, role change, block and unblock, auto sign-out setting |
| Teams (Web) | Team creation, rename and deletion, members added or removed, clusters added or moved |
| Clusters | Cluster added, updated, deleted, credentials replaced; cluster sign-in requested, completed, failed or revoked |
| Resources | Apply YAML, delete, scale, restart, rollback, retry, evict |
| Interactive access | Terminal, node shell and port-forward opened and closed; debug containers created |
| Sensitive reads | Secret values viewed, Helm release values and manifest viewed |
| GitOps | Argo CD sync, refresh, terminate, rollback, auto-sync changes, token changes, application deletion; Flux reconcile, suspend, resume and deletion, and views of Flux data |
| Platform | Metrics stack installed or removed, audit log viewed and exported |

### When the log cannot be written

If the log store fails, a banner says: **The log is not being written right now. Actions are performed but not recorded.** Treat it as an incident: fix the cause (disk or database) and review what happened in the meantime from your other logs.

### Where it lives and how long it is kept

- **Desktop:** `audit.log` in the [data folder](/guide/desktop#where-kubron-keeps-its-data).
- **Web with PostgreSQL:** in the database, kept forever unless you set `KUBRON_AUDIT_RETENTION`. Set `KUBRON_AUDIT_STDOUT=1` to copy every entry to standard output for an external collector. See [Logging, audit and metrics](/server/observability#audit-to-stdout) and [Configuration](/server/configuration).
- Back up the audit log together with the rest of the database: [Backup, restore and key rotation](/server/backup).
