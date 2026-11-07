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
