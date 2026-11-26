# Report a problem

The **Report a problem** button in the header sends a bug report to the Kubron developers, with a screenshot of the window and a few technical facts. No account is needed. A report is **never sent automatically**, for example when an error occurs: it leaves your computer only when you press **Send**.

[[toc]]

## Send a report

1. Choose **Report a problem**. Kubron takes a screenshot of the window **before** the dialog opens, so the screenshot shows the error and not the dialog.
2. Check the preview. Clear **Attach the screenshot** to send without it, or choose **Retake screenshot**.
3. In **What happened?** describe what you did, what you expected and what you saw instead. The description is required and can have up to 5,000 characters.
4. Optionally enter **Your e-mail** so the developers can ask questions.
5. Choose **Send**.

The dialog then says **The report was sent. Thank you!**, or, on a computer without a connection, **No connection: the report is saved on this computer and will be sent automatically.**

::: warning Check the screenshot
The screenshot may show names, addresses, namespaces or secrets that were on the screen. Check it before sending, or turn the attachment off.
:::

## What is sent

| Sent | Not sent |
|---|---|
| Your description and the optional e-mail | Cluster names and addresses |
| The screenshot, if you kept it (PNG up to 6 MiB) | Credentials |
| Kubron version and whether it is Desktop or Web | Logs |
| Operating system and architecture | |
| Interface language and the name of the open section | |
| The login of the signed-in user (Web only) | |

The only place that can reveal cluster details is the screenshot, so it is the part to check.

## Where the report goes

Kubron tries the delivery channels in order and stops at the first one that works:

1. **A mail server**, if one is configured. A company mail server works without internet access.
2. **The project receiver**, a function at `https://kubron.pages.dev/api/bugreport`. It stores the report in a **private** GitHub repository: the screenshot as a file and an issue that links to it. The access token lives only in the receiver's secrets, never in Kubron.

| Edition | Default |
|---|---|
| **Desktop** | Uses the project receiver. You can add a mail server in the dialog |
| **Web** | **Nothing is sent** until an administrator sets a mail server or a receiver. Without one, the button is hidden |

So on Desktop, pressing **Send** transmits the report to the Kubron project's receiver, and only then. Kubron does not send reports by itself.

## No connection

If there is no connection, Desktop saves the report in the `bugreports` folder in the [data folder](/guide/desktop#where-kubron-keeps-its-data) and sends it automatically: one minute after the next start, then every 15 minutes. Saved reports are readable only by you (mode `0600`), and Kubron keeps at most the newest 20 and removes any older than 14 days. A report that the receiver **refused** is not kept, because sending it again would not change the answer.

If reports are waiting, the dialog shows how many (**3 report(s) are waiting to be sent**) and a **Save as archive** button. It writes one `.zip` with a folder per report (the text, the screenshot and the facts) and shows it in the file manager, so you can pass it to the developers by any means. The reports stay in the queue and are still sent if a connection appears.

## Mail server (Desktop)

Open **Mail server settings** in the dialog.

| Field | Notes |
|---|---|
| **Server**, **Port** | Port 587 by default (465 for TLS, 25 without encryption) |
| **Connection** | **STARTTLS**, **TLS (SSL)**, or **No encryption (this machine only)** for a server on the same computer |
| **User**, **Password** | The sign-in. A user needs a password. For Gmail, use an [app password](https://support.google.com/accounts/answer/185833), not the account password |
| **From address** | Defaults to the user. Gmail requires the signed-in account |
| **Send reports to** | The recipient |

Settings are saved in `bugreport.json` in the data folder (mode `0600`), and the password is encrypted with Kubron's master key. If the key is not available, Kubron says the settings cannot be saved here and asks you to use environment variables instead. The `KUBRON_BUGREPORT_SMTP_*` variables are also read; **saved settings take priority**. **Remove saved settings** returns to the variables.

::: warning Set the recipient
If you configure a mail server and do not set a recipient, reports go to the **project's own mailbox**, not to you. Always set **Send reports to** (`KUBRON_BUGREPORT_SMTP_TO`) when you want reports to stay inside your organization.
:::

## Web: for administrators

The administrator sets the mail server or the receiver with environment variables; users cannot read or change them. The button appears once `KUBRON_BUGREPORT_SMTP_HOST` or `KUBRON_BUGREPORT_RELAY_URL` is valid. A server without internet access should use the mail server of your own organization. If both channels are set, mail is tried first. With a `NetworkPolicy`, allow outbound traffic to the mail server (its SMTP port) or HTTPS to the receiver. See [Docker and Kubernetes deployment](/server/deploy#bug-reports) and the [configuration reference](/server/configuration).

`KUBRON_BUGREPORT_RELAY_URL=off` disables the receiver explicitly.

## Limits and audit

- One report per user every **30 seconds**. The Web server also stops at **30 reports an hour** in total.
- The project receiver accepts **10 reports an hour from one address** and **200 a day** in total.
- Each sent report is written to the [audit log](/features/administration#audit-log) as **bugreport.submit**, **without its text**.

## Messages

| Message | Meaning |
|---|---|
| **The mail server is not set up yet. Fill in the settings below to send reports.** | No delivery channel is available. On Desktop this happens only when the receiver is turned off (`KUBRON_BUGREPORT_RELAY_URL=off`) and no mail server is set |
| **The mail server for reports is not set up. Ask the administrator to set KUBRON_BUGREPORT_SMTP_\*.** | Web: an administrator has to configure it |
| **The screenshot could not be taken. The report can be sent without it.** | Send without the screenshot |
| **No connection: the report is saved on this computer and will be sent automatically.** | Desktop queued the report |
