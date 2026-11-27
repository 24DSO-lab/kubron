# Cloudflare donation ledger

The public ledger is served by the `kubron-donations` Worker and persisted in D1.
The Pages Function at `/api/donations` proxies the public response to the Worker;
the website never reads the repository's static `public/donations.json`.

## Deployment

The D1 database `kubron-donations` is already provisioned and its database ID is
configured in `wrangler.donations.toml`.

The repository already has the required GitHub Actions secrets:

- `CLOUDFLARE_API_TOKEN` with Workers Scripts edit and D1 edit permissions.
- `CLOUDFLARE_ACCOUNT_ID`.
- `DONATIONALERTS_SYNC_SECRET` (the same value configured for the Pages Function).
- `TRONGRID_API_KEY`.

After this PR is merged into `refactor`, the deployment workflow applies the D1
migration, configures the Worker secrets and deploys the Worker. It runs every
15 minutes through a Cloudflare Cron Trigger. A manual authenticated sync is also
available at `POST /internal/sync`.

Then set the Cloudflare Pages **Production** environment variable
`DONATIONS_WORKER_URL` to the deployed Worker URL
(for example, `https://kubron-donations.<workers-subdomain>.workers.dev`).
This is a regular environment variable, not a secret. Redeploy Pages after setting it.

The first migration preserves the already verified 10 RUB DonationAlerts entry.
Subsequent syncs are idempotent by transaction ID.

## Data policy

Only confirmed transactions from BTC, ETH, USDC (Ethereum), USDT (TRON), and
DonationAlerts are inserted. The Worker keeps the existing confirmation thresholds:
3 blocks for Bitcoin and 12 blocks for Ethereum. The public endpoint returns only
the last 30 days, newest first, and does not expose donor email or donation messages.
