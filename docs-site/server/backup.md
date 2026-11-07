# Backup, restore and key rotation

The Web edition with PostgreSQL keeps its state in two parts. **Back them up and store them separately.**

| What | Where it lives | How to back it up |
|---|---|---|
| Users, teams, cluster metadata, audit log, Helm state, web sessions, and the **encrypted** kubeconfigs and secrets | PostgreSQL database | `pg_dump` or storage snapshots |
| The encryption key `KUBRON_SECRET_KEY` (and earlier keys) | Kubernetes Secret or secret manager | A copy in a password manager or KMS, **not** next to the database backup |

A database dump without the key is useless for secrets: kubeconfigs, cluster passwords, Argo CD tokens and Helm repository secrets cannot be decrypted. A key without a dump is useless too. Keeping the key next to the database backup (same storage, same access) defeats the encryption. The chart does **not** generate the key, so losing it means losing every secret in the database; the only recovery is to add the clusters again.

[[toc]]

## Back up

### The database

The custom format (`-Fc`) allows selective and parallel restore:

```bash
pg_dump -Fc --no-owner --no-privileges -f kubron-$(date +%F).dump "$KUBRON_DATABASE_URL"
```

- Your tolerable data loss decides the schedule. A daily dump plus WAL archiving or point-in-time recovery from your database or cloud is usually enough for the audit log and team membership.
- Encrypt backups at rest and in transit and restrict access. They contain password hashes, the audit log and ciphertext of secrets.
- Web sessions (`web_sessions`) are not needed after a restore: users simply sign in again. To keep them out of a test environment, run `TRUNCATE web_sessions;` on the copy.
- The chart does not create a backup CronJob. Run `pg_dump` to object storage from your own infrastructure.

### The key

Keep the key in a password manager, a KMS or a sealed envelope, with access limited to a few people. Store:

- the key itself (`id:base64`) and its `id`;
- **every earlier key for as long as backups made before the rotation exist** (see [Rotate the key](#rotate-the-key-and-rekey)), because a backup is read with the keys that encrypted its records.

To see which key ids the data needs:

```sql
SELECT 'clusters.kubeconfig' AS data, key_id AS key_id, count(*) FROM clusters WHERE kubeconfig_enc IS NOT NULL GROUP BY key_id
UNION ALL SELECT 'clusters.secret', secret_key_id, count(*) FROM clusters WHERE secret_enc IS NOT NULL GROUP BY secret_key_id
UNION ALL SELECT 'argo_tokens', key_id, count(*) FROM argo_tokens GROUP BY key_id
UNION ALL SELECT 'helm_repositories', secret_key_id, count(*) FROM helm_repositories WHERE secret_enc IS NOT NULL GROUP BY secret_key_id
UNION ALL SELECT 'cluster_credentials', secret_key_id, count(*) FROM cluster_credentials GROUP BY secret_key_id;
```

Write the id of the current key next to every backup (for example in the file name), so a restore immediately shows which key to fetch.

## Restore

1. Create an empty database and restore the dump:

   ```bash
   createdb kubron
   pg_restore --no-owner --no-privileges --exit-on-error -d kubron kubron-2026-11-01.dump
   ```

2. Create the Secret with **the same key** (same id and same bytes). If the dump still contains records under earlier keys, add them to `KUBRON_SECRET_KEY_PREVIOUS` (comma-separated).
3. Install the chart, or start the server, with `KUBRON_DATABASE_URL` pointing at the restored database. Migrations apply automatically if the dump came from an older schema.
4. Check that `/healthz` and `/readyz` return `200`, that a known account can sign in, and that a team's cluster opens.

At startup the server reads one encrypted record for every key id and **refuses to start** if the key is missing or wrong. The message names the cause:

| Message | Cause | What to do |
|---|---|---|
| `…KUBRON_SECRET_KEY does not match…: …stored with encryption key "k1"…: ciphertext was encrypted with an unknown key` | No key with this id is in the key set | Add key `k1` to `KUBRON_SECRET_KEY` (if it was the current one) or to `KUBRON_SECRET_KEY_PREVIOUS` |
| `…: failed to decrypt the secret` | The id matches but the key bytes differ (a key from another environment, or a typo) | Use the key that encrypted this database |

The message never contains the key or decrypted data. During a `RollingUpdate`, a replica with the wrong key never becomes ready, while the old replicas keep serving.

### The key is lost for good

If the key is gone but you still need the database, for example to export the audit log, start the server with `KUBRON_ALLOW_KEY_MISMATCH=1`. It starts, users and teams work, and cluster secrets stay unavailable. Add the clusters again with new kubeconfigs afterwards. Do not leave this mode on permanently.

## Rotate the key and `rekey`

New records are encrypted with the current key. Old records are read with earlier keys until they are re-encrypted.

1. Generate a new key with a **new id** (`id:base64`, 32 bytes) and store it in your secret manager **before** using it:

   ```bash
   echo "2026-11:$(openssl rand -base64 32)"
   ```

2. In the Secret, put the new key in `KUBRON_SECRET_KEY` and the old one in `KUBRON_SECRET_KEY_PREVIOUS` (comma-separated if there are several). In the chart, `secrets.keys.*` only set the **names** of entries inside the Secret; to make the server read the old key, set `secrets.keys.previousSecretKey: KUBRON_SECRET_KEY_PREVIOUS` (empty by default). Restart every replica (`kubectl rollout restart deploy/kubron`). The server starts and reads data with both keys.
3. Re-encrypt the records with the new key. The command reads the database from the server's environment, so run it where that environment exists:

   ```bash
   kubectl exec deploy/kubron -- /kubron-server rekey
   # re-encrypted with key "2026-11": 12 kubeconfigs, 3 cluster secrets, 2 Argo CD tokens, 4 Helm repository secrets, 5 sign-in credentials (0 still in legacy format)
   ```

   `rekey` is safe to repeat: records that are already re-encrypted are skipped. The image has no shell, so run the command directly, or in a one-off Job with the same environment.
4. Make sure the old id is no longer needed: the queries from [The key](#the-key) should show only the new `key_id`.
5. You may then remove the old key from `KUBRON_SECRET_KEY_PREVIOUS`, but **keep it for as long as backups made before `rekey` exist**, because their records are encrypted with it. The retention period of a key equals the retention period of the oldest backup you want to be able to restore.

`rekey` re-encrypts kubeconfigs, cluster connection secrets, Argo CD tokens, Helm repository secrets and saved sign-in credentials. Rotate the key on a schedule (for example once a year) and whenever you suspect compromise. If a key is compromised, also replace the cluster secrets themselves (tokens, passwords) after `rekey`: backups older than the rotation stay decryptable with the old key.

## Verify your backups

The project verifies restores automatically: the `restore-check` CI job runs `scripts/restore-check.sh`. It starts a server on a source database, takes a `pg_dump`, restores it into an empty database, starts a second server with the same key and checks that sign-in works and the kubeconfig matches. It also checks that a wrong key stops startup with a clear message, that `KUBRON_ALLOW_KEY_MISMATCH=1` withholds secrets, that rotation plus `rekey` works, and that logs contain no tokens or key bytes.

To run it by hand you need PostgreSQL, `pg_dump`, `pg_restore`, `psql`, `curl`, `jq` and `openssl`:

```bash
go build -o kubron-server ./cmd/kubron-server
PGHOST=127.0.0.1 PGUSER=postgres PGPASSWORD=postgres KUBRON_SERVER_BIN=./kubron-server scripts/restore-check.sh
```

The script creates and drops the databases `kubron_restore_src` and `kubron_restore_dst`.

That does not replace your own drill. **Once a quarter**, restore your latest real backup on a separate server with the key from your secret manager, and check sign-in and cluster access. It is the only way to know that the backup and the key are both stored and match.

## Single-user and Desktop

Without PostgreSQL, data lives in the data directory (`~/.kubron` or `KUBRON_DATA_DIR`). Copy the whole directory, as before. This page applies to the PostgreSQL mode.
