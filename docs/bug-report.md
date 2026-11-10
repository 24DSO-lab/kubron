# Report a problem

The **Report a problem** button in the header of the desktop app and of the web interface sends a bug report to the
developers. Decisions and alternatives: [ADR-0029](adr/0029-bug-report.md) and
[ADR-0030](adr/0030-bug-report-receiver.md).

## What the user does

1. Presses the button. The app takes a screenshot of the window *before* the dialog opens, so the screenshot shows
   the error and not the dialog.
2. Checks the preview (it can be turned off or retaken), writes what happened and, optionally, an e-mail for questions.
3. Presses **Send**. The screenshot and the facts below are added automatically. No account is needed.

The dialog then says either **sent**, or **saved**: there was no connection, so the report waits on this computer and
is sent by itself later.

## Where the report goes

The core tries the ways in this order and stops at the first that works:

1. **A mail server**, if one is set (see below). A company mail server works without internet access.
2. **The receiver of the project**: a Pages Function at `https://kubron.pages.dev/api/bugreport`. It stores the report
   in a **private** repository: the screenshot as a file and an issue that links to it. The desktop uses it by default;
   the web server only when the administrator sets `KUBRON_BUGREPORT_RELAY_URL`. The value `off` turns it off.

A report that could not be delivered because there is no connection is kept in `<data directory>/bugreports/` (desktop
only; mode 0600, files older than 14 days and all but the newest 20 are removed). The app tries again a minute after the
start and then every 15 minutes. A report that the receiver refused is not kept: repeating would not change the answer.

### A computer without any connection

If reports are waiting, the dialog shows how many and a **Save as archive** button. It writes one `.zip` (a folder with
the text, the screenshot and the facts for each report) and shows it in the file manager. Pass the file to the developers
by any means. The reports stay in the queue and are still sent if a connection appears.

## What is sent

The text, the screenshot (if kept), the optional contact address and a few facts: application version, OS and
architecture, interface language, the name of the open section. Cluster names, addresses, credentials and logs are
not added. The screenshot itself may show them; the dialog warns about this.

## Mail server

There are no credentials in the application.

- **Desktop:** set them once in the dialog (**Mail server settings**) or with environment variables. Saved settings take
  priority.
- **Web server:** the administrator sets the variables below (see
  [Bug reports](../docs-site/server/deploy.md#bug-reports)). The button is hidden until `KUBRON_BUGREPORT_SMTP_HOST` or
  `KUBRON_BUGREPORT_RELAY_URL` is valid, and users cannot read or change the mail account.

| Variable | Meaning | Default |
|---|---|---|
| `KUBRON_BUGREPORT_SMTP_HOST` | server; without it nothing is sent | — |
| `KUBRON_BUGREPORT_SMTP_PORT` | port | 587 (465 for `tls`, 25 for `none`) |
| `KUBRON_BUGREPORT_SMTP_SECURITY` | `starttls`, `tls` or `none` (`none` only for a server on this machine) | `starttls` |
| `KUBRON_BUGREPORT_SMTP_USER`, `KUBRON_BUGREPORT_SMTP_PASSWORD` | sign-in; the password is required with a user | none |
| `KUBRON_BUGREPORT_SMTP_FROM` | sender address | the user |
| `KUBRON_BUGREPORT_SMTP_TO` | recipient | `224vadim224@gmail.com` |
| `KUBRON_BUGREPORT_RELAY_URL` | address of the report receiver (HTTPS); `off` disables it | desktop: the project receiver; server: none |

For Gmail use `smtp.gmail.com`, port 587 and an [app password](https://support.google.com/accounts/answer/185833),
not the account password.

Saved settings live in `bugreport.json` in the data directory (mode 0600, the password is sealed with the master key).

## Limits

Description up to 5000 characters, screenshot up to 6 MiB (PNG; the browser shrinks it to fit the 4 MiB request limit of
the web server). One report per user every 30 seconds; the web server also stops at 30 reports an hour (mail server). The receiver of the project accepts 10 reports an hour from one address and 200 a day in total. Each sent report is
written to the audit log (`bugreport.submit`) without its text.

## Setting up the receiver (maintainers)

See `docs-site/DOMAIN.md`: the private reports repository, the token and the Cloudflare Pages settings.
