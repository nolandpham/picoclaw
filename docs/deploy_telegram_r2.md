# Deploy Guide: Telegram + Cloudflare R2 Sync

This guide covers a practical production path for running PicoClaw with:

- Telegram chat via `gateway`
- Local file-state sync to Cloudflare R2 every 5 minutes

## 1) Prerequisites

- Linux server/container with network access
- PicoClaw built binary (`make build`) or installed binary
- Telegram bot token from `@BotFather`
- Your Telegram user ID (for `allow_from`) from `@userinfobot`
- Cloudflare R2 bucket + S3 credentials:
  - `account_id`
  - `access_key_id`
  - `secret_access_key`
  - `bucket`

## 2) Configure `~/.picoclaw/config.json`

Minimum recommended config:

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.picoclaw/workspace",
      "restrict_to_workspace": true,
      "provider": "gemini",
      "model": "gemini-2.0-flash"
    }
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "proxy": "",
      "allow_from": ["YOUR_TELEGRAM_USER_ID"]
    }
  },
  "sync": {
    "enabled": true,
    "interval_minutes": 5,
    "mode": "r2-first",
    "r2": {
      "enabled": true,
      "account_id": "YOUR_CLOUDFLARE_ACCOUNT_ID",
      "access_key_id": "YOUR_R2_ACCESS_KEY_ID",
      "secret_access_key": "YOUR_R2_SECRET_ACCESS_KEY",
      "bucket": "YOUR_R2_BUCKET",
      "prefix": "picoclaw-state",
      "sync_workspace": true,
      "sync_sessions": true,
      "sync_memory": true,
      "sync_config": true,
      "sync_auth": true,
      "sync_skills": true
    }
  }
}
```

Notes:

- `sync.mode` is currently `r2-first` oriented.
- `interval_minutes` should be `5` for your requested cadence.
- Keep `allow_from` non-empty for safer Telegram access control.

## 3) Build and run

```bash
cd /workspaces/picoclaw
make build
./build/picoclaw gateway
```

Expected startup signals:

- `✓ Channels enabled: [telegram]` (or includes telegram)
- `✓ Heartbeat service started`
- `✓ R2 state sync service started`

## 4) Telegram smoke test

1. Open your bot chat in Telegram.
2. Send `/start`.
3. Send a normal message, e.g. `2+2?`.
4. Verify PicoClaw replies in the same chat.

If no reply:

- Re-check `channels.telegram.token`.
- Re-check your user ID in `allow_from`.
- Confirm gateway process is still running.

## 5) R2 sync smoke test (5-minute cadence)

### A. Local -> R2

1. Create/update a state file, for example:

```bash
echo "sync check $(date -Iseconds)" >> ~/.picoclaw/workspace/HEARTBEAT.md
```

2. Wait up to 5 minutes.
3. Verify object appears/updates in R2 under prefix:

- `picoclaw-state/files/workspace/HEARTBEAT.md`
- `picoclaw-state/manifest.json`

### B. R2 -> Local (R2-first behavior)

1. Update the same object in R2 with newer content/timestamp.
2. Wait up to 5 minutes.
3. Verify local file is updated accordingly.

## 6) Operational recommendations

- Run under `systemd`/supervisor for auto-restart.
- Protect credentials in secrets manager or environment variables.
- Back up R2 bucket lifecycle/versioning if required.
- Keep heartbeat and cron enabled as independent services:
  - Heartbeat default: 30 minutes
  - Cron due-check: internal loop handles scheduled jobs continuously

## 7) Troubleshooting

### `Error creating state sync service`

Usually missing/invalid R2 settings (`account_id`, keys, bucket).

### Telegram enabled but no incoming messages

- Invalid bot token
- User blocked by `allow_from`
- Network restriction to Telegram API

### Sync appears idle

- Ensure `sync.enabled=true` and `sync.r2.enabled=true`
- Confirm process logs include sync startup and periodic sync entries
- Verify bucket/prefix permissions for both read and write
