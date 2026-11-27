# Cloudflare donation ledger

The public ledger is served by the `kubron-donations` Worker and persisted in D1.
The Pages Function at `/api/donations` proxies the public response to the Worker;
the website never reads the repository's static `public/donations.json`.

## One-time Cloudflare setup

1. Create a D1 database named `kubron-donations` in the same Cloudflare account as Pages.
2. Copy its database ID into `database_id` in `wrangler.donations.toml`.
3. Add these GitHub Actions repository secrets:
   - `CLOUDFLARE_API_TOKEN` with Workers Scripts edit and D1 edit permissions.
   - `CLOUDFLARE_ACCOUNT_ID`.
   - `DONATIONALERTS_SYNC_SECRET` (the same value already configured for the Pages Function).
   - `TRONGRID_API_KEY`.
4. Add the Pages environment variable `DONATIONS_WORKER_URL` with the Worker URL
   (for example, `https://kubron-donations.<workers-subdomain>.workers.dev`).
5. Deploy the Worker workflow. It applies the D1 migration before deploying and
   configures the two Worker secrets.

The first migration preserves the already verified 10 RUB DonationAlerts entry.
Subsequent syncs are idempotent by transaction ID. The Worker runs every 15 minutes;
a manual authenticated sync is also available at `POST /internal/sync`.

## Data policy

Only confirmed transactions from BTC, ETH, USDC (Ethereum), USDT (TRON), and
DonationAlerts are inserted. The Worker keeps the existing confirmation thresholds:
3 blocks for Bitcoin and 12 blocks for Ethereum. The public endpoint returns only
the last 30 days, newest first, and does not expose donor email or donation messages.
