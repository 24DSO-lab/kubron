-- Copyright 2026 The Kubron Authors
-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE IF NOT EXISTS donations (
  id TEXT PRIMARY KEY,
  asset TEXT NOT NULL,
  network TEXT NOT NULL,
  amount TEXT NOT NULL,
  tx_hash TEXT NOT NULL,
  from_address TEXT,
  donor_name TEXT,
  received_at TEXT NOT NULL,
  explorer_tx_url TEXT,
  source TEXT,
  status TEXT NOT NULL CHECK (status = 'confirmed'),
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_donations_received_at ON donations(received_at DESC);
CREATE INDEX IF NOT EXISTS idx_donations_source ON donations(source);

-- Preserve the already verified DonationAlerts contribution during migration.
INSERT OR IGNORE INTO donations
  (id, asset, network, amount, tx_hash, received_at, source, status)
VALUES
  ('donationalerts:194853098', 'RUB', 'DonationAlerts', '10', '194853098',
   '2026-11-27T13:33:00.000Z', 'donationalerts', 'confirmed');
