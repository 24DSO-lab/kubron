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

## GitHub Pages

The repository workflow is [.github/workflows/docs.yml](../.github/workflows/docs.yml). It builds the site from `docs-site/` and deploys the artifact to GitHub Pages. In repository settings, select **Settings → Pages → Build and deployment → GitHub Actions**.

The workflow sets `DOCS_BASE=/kubron/` for the project Pages URL. For a custom domain, set `DOCS_BASE=/` and configure the domain in Pages settings.

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
