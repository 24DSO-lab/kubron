# Kubron documentation site

VitePress documentation for [Kubron](https://github.com/24DSO-labs/kubron). English is the default locale; Russian is available under `/ru/`.

## Local development

```bash
cd docs-site
npm ci
npm run docs:dev
```

## Production build

```bash
npm run docs:build
npm run docs:preview
```

The static site is generated into `.vitepress/dist`.

## Deployment

The production site is hosted on [Cloudflare Pages](https://kubron.pages.dev/). The GitHub Actions workflow at `.github/workflows/docs.yml` builds the site and deploys it with Wrangler.

- Production branch: `refactor`
- Build command: `npm ci && npm run docs:build` (from `docs-site/`)
- Build output: `docs-site/.vitepress/dist`
- VitePress base path: `/` (`DOCS_BASE=/`)

Pull requests targeting `refactor` run the build but do not publish the site. Pushes to `refactor` deploy after a successful build. The Cloudflare Pages project must have `refactor` configured as its production branch; disable Cloudflare's Git integration builds so GitHub Actions remains the only deployment path.

### GitHub repository secrets

Configure these under **Settings → Secrets and variables → Actions**:

- `CLOUDFLARE_API_TOKEN`: API token with Account → Cloudflare Pages → Edit.
- `CLOUDFLARE_ACCOUNT_ID`: Cloudflare account ID.

Do not store either value in the repository.

## Structure

```
docs-site/
├── .vitepress/       # VitePress config and theme
├── guide/            # English user guide
├── features/         # English feature reference
├── server/           # Server mode
├── development/      # Contributor documentation
├── ru/               # Russian versions of the sections above
└── public/           # Brand assets
```

Screenshots and the social preview are reused from `docs/images/` in the main repository so that documentation reflects the same product imagery as the README.

## Keeping docs accurate

When changing a user-facing feature, update the corresponding English and Russian pages in the same pull request. Avoid documenting roadmap items as shipped functionality. For server deployment details, keep the full operational guide in sync with `docs/deploy.md`.
