# Cloudflare Worker Production Setup (Independent Runtime)

This guide makes your Telegram bot run independently on Cloudflare Worker (no local PC runtime required).

## 1) What this architecture does

- Receives Telegram updates via webhook: `POST /telegram/webhook`
- Calls OpenAI-compatible chat completion API from Worker
- Sends replies back to Telegram via Bot API
- Emits structured logs via `console.log` for Cloudflare log tracking

## 2) Required secrets (GitHub Environment: `Cloudflare Worker`)

Set these environment secrets in GitHub:

- `CLOUDFLARE_API_TOKEN`
- `CLOUDFLARE_ACCOUNT_ID`
- `WORKER_HEALTH_URL` (optional but recommended for CI smoke test)
  - example: `https://picoclaw-worker.nolandpham.workers.dev/health`

## 3) Required Wrangler secrets (Cloudflare Worker runtime)

From `worker/` directory:

```bash
cd /workspaces/picoclaw/worker
npx wrangler secret put TELEGRAM_BOT_TOKEN
npx wrangler secret put OPENAI_API_KEY
npx wrangler secret put TELEGRAM_WEBHOOK_SECRET
```

Notes:

- `TELEGRAM_WEBHOOK_SECRET` is optional but recommended.
- If you use non-OpenAI endpoint (OpenAI-compatible), set `OPENAI_BASE_URL` in `wrangler.toml`.

## 4) Configure allowlist and model

Edit `worker/wrangler.toml`:

- `ENVIRONMENT = "production"`
- `AI_MODEL = "..."` (your provider model name)
- `OPENAI_BASE_URL = "..."` (OpenAI-compatible base URL)
- `TELEGRAM_ALLOW_FROM = "1849987220"` (comma-separated IDs if multiple users)

Optional: enable lightweight chat memory using Cloudflare KV.

Create namespace:

```bash
cd /workspaces/picoclaw/worker
npx wrangler kv namespace create CHAT_STATE
```

Then add returned IDs into `worker/wrangler.toml`:

```toml
[[kv_namespaces]]
binding = "CHAT_STATE"
id = "<production_namespace_id>"
preview_id = "<preview_namespace_id>"
```

## 5) Deploy Worker

```bash
cd /workspaces/picoclaw/worker
npm ci
npx wrangler deploy --name picoclaw-worker
```

## 6) Register Telegram webhook

Set webhook URL to your worker endpoint:

```bash
curl -sS "https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/setWebhook" \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://picoclaw-worker.nolandpham.workers.dev/telegram/webhook",
    "secret_token": "<TELEGRAM_WEBHOOK_SECRET>"
  }'
```

Verify:

```bash
curl -sS "https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/getWebhookInfo"
```

## 7) Smoke test

- Open bot chat
- Send `/start`
- Send `2+2?`
- Expect AI reply from Worker

Health endpoint check:

```bash
curl -sS https://picoclaw-worker.nolandpham.workers.dev/health
```

## 8) Observability and tracking

- Cloudflare Dashboard → Workers & Pages → your worker → Logs
- Track by `requestId` (included in logs and health response)
- Structured events emitted:
  - `telegram_update_received`
  - `telegram_reply_sent`
  - `telegram_reply_failed`
  - `telegram_user_blocked`
  - `telegram_webhook_secret_mismatch`

## 9) Cutover checklist

- Stop local process: `pkill -f "picoclaw gateway"`
- Confirm webhook is set to Worker URL
- Confirm `/health` returns `"env":"production"`
- Confirm Telegram message round-trip succeeds

## 10) Rollback

- Repoint Telegram webhook to previous endpoint (if any), or
- Disable webhook and switch back to polling runtime:

```bash
curl -sS "https://api.telegram.org/bot<TELEGRAM_BOT_TOKEN>/deleteWebhook"
```
