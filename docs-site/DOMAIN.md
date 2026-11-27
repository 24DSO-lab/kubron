# Documentation hosting

The production documentation site is hosted on Cloudflare Pages:

- URL: https://kubron.pages.dev/
- Cloudflare Pages project: `kubron`
- Production branch: `refactor`

## Deployment through GitHub Actions

The canonical deployment workflow is `.github/workflows/docs.yml`.

1. In Cloudflare Pages, set `refactor` as the production branch.
2. Disable Cloudflare Git integration builds/deployments. GitHub Actions is the only deployment controller.
3. Add `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` as GitHub Actions repository secrets.
4. The workflow runs the VitePress build for pull requests targeting `refactor`.
5. A push to `refactor` deploys the already-built artifact using Wrangler.

The workflow uses `DOCS_BASE=/`, which is required for the root path on `kubron.pages.dev`.

## Local build

```bash
cd docs-site
npm ci
DOCS_BASE=/ npm run docs:build
npm run docs:preview
```

The static output is generated in `.vitepress/dist`.

## Bug report receiver

The Pages Function `functions/api/bugreport.ts` (logic in `relay/bugreport.ts`) receives the reports of the
**Report a problem** button and stores them in a **private** GitHub repository. Design: ADR-0030.

1. Create a private repository for the reports (for example `24DSO-lab/kubron-reports`) with at least one commit
   (a README is enough). Turn on issues.
2. Create a fine-grained personal access token limited to that repository with **Contents: read and write** and
   **Issues: read and write**. It expires after at most a year: put a reminder in the calendar.
3. In Cloudflare Pages, project `kubron`, **Settings → Variables and Secrets** (production):
   - secret `REPORTS_GITHUB_TOKEN`: the token;
   - variable `REPORTS_REPO`: `owner/name` of the reports repository;
   - variable `REPORTS_BRANCH`: the branch for screenshots (default `main`).
4. In **Settings → Bindings** add a KV namespace with the variable name `RATE` (it holds the request counters). Without
   it the receiver refuses every report on purpose.
5. Deploy through the workflow as usual. Check with an empty report:
   `curl -i -X POST -H 'content-type: application/json' -d '{}' https://kubron.pages.dev/api/bugreport`.
   A configured receiver answers `400` (the report is not valid); `503` means the token, the repository or the `RATE`
   binding is missing, and `405` or the documentation page means the Function was not deployed.

Limits: 10 reports an hour per address and 200 a day in total; the free Cloudflare plan allows 100,000 Functions
requests a day.

## DonationAlerts OAuth

The DonationAlerts OAuth routes are Pages Functions under `functions/api/donationalerts/`; implementation and tests
are in `relay/donationalerts.ts` and `relay/donationalerts.test.ts`.

1. Register an OAuth app at https://www.donationalerts.com/application/clients.
2. Set its redirect URI to exactly `https://kubron.pages.dev/api/donationalerts/callback`.
3. Request the least-privilege scope `oauth-donation-index` (read donation history).
4. In Cloudflare Pages → project `kubron` → **Settings → Bindings**, create a dedicated KV namespace binding named
   `DONATIONALERTS`. Do not reuse the bug-report `RATE` namespace.
5. In **Settings → Variables and Secrets** (production), configure:
   - `DONATIONALERTS_CLIENT_ID`: numeric OAuth client ID;
   - `DONATIONALERTS_CLIENT_SECRET`: secret OAuth client secret;
   - `DONATIONALERTS_TOKEN_ENCRYPTION_KEY`: random secret of at least 32 characters;
   - `DONATIONALERTS_REDIRECT_URI`: optional, but if set it must exactly match the callback URI above.

Generate the encryption key with `openssl rand -base64 48`. The OAuth tokens are encrypted with AES-GCM before storage in KV.
Never commit or share OAuth credentials.

After deployment and configuration, open `https://kubron.pages.dev/api/donationalerts/connect` to authorize the Kubron
account. The callback confirms success without exposing tokens. `GET /api/donationalerts/status` returns only a boolean
connection status.

This milestone implements OAuth authorization and secure token storage only. Automatic donation synchronization and
publishing DonationAlerts entries in the public ledger are not enabled yet; they will be added as a separate step after
the OAuth application is registered and the account is connected.

The relay tests run in the workflow before the build: `npm run test:relay`.
