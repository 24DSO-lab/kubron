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

The receiver tests run in the workflow before the build: `npm run test:relay`.
