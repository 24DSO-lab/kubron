# Documentation hosting

The repository already contains the GitHub Pages workflow at `.github/workflows/docs.yml`. It builds `docs-site/` and publishes the static site.

## GitHub Pages

1. Open **Settings → Pages** in the repository.
2. Set **Build and deployment → Source** to **GitHub Actions**.
3. Push changes to `docs-site/**` on `master` or run the workflow manually.
4. The project site is served at `https://24dso-labs.github.io/kubron/`.

The workflow sets `DOCS_BASE=/kubron/`, which is required for a project Pages URL.

## Custom domain

For a custom domain such as `docs.example.com`:

1. Configure the domain in **Settings → Pages → Custom domain**.
2. Add the DNS records GitHub provides and wait for HTTPS provisioning.
3. Change `DOCS_BASE` to `/` in `.github/workflows/docs.yml`.
4. Commit and deploy the change.

## Alternative: Cloudflare Pages

Cloudflare Pages can also deploy this repository:

- Build command: `cd docs-site && npm ci && npm run docs:build`
- Output directory: `docs-site/.vitepress/dist`
- Environment variable: `DOCS_BASE=/`

Use the GitHub Pages workflow as the canonical deployment path unless there is a specific reason to host the site elsewhere.
