# Kubron documentation site

VitePress site for [Kubron](https://github.com/24DSO-labs/kubron).

## Local development

```bash
cd docs-site
npm install
npm run docs:dev
```

Open the URL printed by VitePress (usually http://localhost:5173).

## Build

```bash
npm run docs:build
# output → .vitepress/dist
```

## Deploy

1. Copy the whole `docs-site/` folder into the root of the `kubron` repository.
2. Copy `.github-workflow-docs.yml` → `.github/workflows/docs.yml`.
3. In the repository settings:
   - **Settings → Pages → Build and deployment → Source**: GitHub Actions
4. Push to `master`/`main` (or run the workflow manually).

Default `DOCS_BASE` in the workflow is `/kubron/` →  
**https://24dso-labs.github.io/kubron/**

For a custom domain set `DOCS_BASE=/` and configure the domain in Pages settings.

## Structure

```
docs-site/
├── .vitepress/          # config + theme
├── guide/               # English guide
├── features/
├── server/
├── development/
├── ru/                  # Russian (partial)
├── public/              # logo, favicon
├── index.md             # English home
└── package.json
```

## Free domain options

See `DOMAIN.md` in this folder.
