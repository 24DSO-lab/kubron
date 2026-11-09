# Report a problem

The **Report a problem** button in the header of the desktop app and of the web interface sends a bug report by e-mail. Decision and
alternatives: [ADR-0029](adr/0029-bug-report.md).

## What the user does

1. Presses the button. The app takes a screenshot of the window *before* the dialog opens, so the screenshot shows
   the error and not the dialog.
2. Checks the preview (it can be turned off or retaken), writes what happened and, optionally, an e-mail for questions.
3. Presses **Send**, or on the desktop **Open in mail app** (see below).

## Desktop: your mail app, no mail server

On the desktop, **Open in mail app** works without any setup. It opens the default mail program with a message to the
recipient (subject, description and facts filled in); the user checks it and presses Send there.

A mail link cannot carry an attachment, so:

- the screenshot is **copied to the clipboard**: click in the message and paste (`Ctrl+V`, `⌘V` on macOS);
- it is also **saved as a file** in `<data directory>/bugreports/` (mode 0600; files older than 7 days and all but the
  newest 20 are removed); the dialog shows the path and a **Show in folder** button, for the case that pasting does not work
  (some older WebKitGTK builds cannot put an image on the clipboard);
- a description that does not fit into the link (about 1900 characters, which is about 250 Cyrillic letters) is shortened
  in the message, and the full text is saved next to the screenshot as a `.txt` file.

If nothing opens, no mail program is set as the default for e-mail links.

## What is sent

The text, the screenshot (if kept), the optional contact address and a few facts: application version, OS and
architecture, interface language, the name of the open section. Cluster names, addresses, credentials and logs are
not added. The screenshot itself may show them; the dialog warns about this.

## Mail server

There are no credentials in the application.

- **Desktop:** set them once in the dialog (**Mail server settings**) or with environment variables. Saved settings take
  priority.
- **Web server:** the administrator sets the variables below (see
  [Bug reports](../docs-site/server/deploy.md#bug-reports)). The button is hidden until `KUBRON_BUGREPORT_SMTP_HOST` is
  valid, and users cannot read or change the mail account.

| Variable | Meaning | Default |
|---|---|---|
| `KUBRON_BUGREPORT_SMTP_HOST` | server; without it nothing is sent | — |
| `KUBRON_BUGREPORT_SMTP_PORT` | port | 587 (465 for `tls`, 25 for `none`) |
| `KUBRON_BUGREPORT_SMTP_SECURITY` | `starttls`, `tls` or `none` (`none` only for a server on this machine) | `starttls` |
| `KUBRON_BUGREPORT_SMTP_USER`, `KUBRON_BUGREPORT_SMTP_PASSWORD` | sign-in; the password is required with a user | none |
| `KUBRON_BUGREPORT_SMTP_FROM` | sender address | the user |
| `KUBRON_BUGREPORT_SMTP_TO` | recipient | `224vadim224@gmail.com` |

For Gmail use `smtp.gmail.com`, port 587 and an [app password](https://support.google.com/accounts/answer/185833),
not the account password.

Saved settings live in `bugreport.json` in the data directory (mode 0600, the password is sealed with the master key).

## Limits

The mail-app way has no limits of its own: the message goes from the user's own account.

Description up to 5000 characters, screenshot up to 6 MiB (PNG; the browser shrinks it to fit the 4 MiB request limit of
the web server). One report per user every 30 seconds; the web server also stops at 30 reports an hour. Each sent report is
written to the audit log (`bugreport.submit`) without its text.
