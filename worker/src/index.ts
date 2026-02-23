export interface Env {
  APP_NAME: string;
  ENVIRONMENT: string;
  AI_MODEL: string;
  OPENAI_BASE_URL: string;
  TELEGRAM_ALLOW_FROM: string;
  TELEGRAM_BOT_TOKEN: string;
  TELEGRAM_WEBHOOK_SECRET?: string;
  OPENAI_API_KEY?: string;
  CHAT_STATE?: KVNamespace;
}

interface KVNamespace {
  get(key: string): Promise<string | null>;
  put(
    key: string,
    value: string,
    options?: { expirationTtl?: number }
  ): Promise<void>;
}

type TelegramUpdate = {
  update_id?: number;
  message?: {
    message_id?: number;
    text?: string;
    chat?: { id?: number };
    from?: { id?: number; username?: string };
  };
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });
}

function extractAllowedUsers(csv: string): Set<string> {
  return new Set(
    (csv || "")
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean)
  );
}

function buildRequestId(request: Request): string {
  return request.headers.get("cf-ray") || crypto.randomUUID();
}

async function sendTelegramMessage(env: Env, chatId: number, text: string): Promise<void> {
  const endpoint = `https://api.telegram.org/bot${env.TELEGRAM_BOT_TOKEN}/sendMessage`;
  const resp = await fetch(endpoint, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      chat_id: String(chatId),
      text,
      disable_web_page_preview: true,
    }),
  });

  if (!resp.ok) {
    const textBody = await resp.text();
    throw new Error(`telegram sendMessage failed: ${resp.status} ${textBody}`);
  }
}

async function completeWithAI(env: Env, userInput: string): Promise<string> {
  if (!env.OPENAI_API_KEY) {
    return "OPENAI_API_KEY chưa được cấu hình trên Worker. Vui lòng thêm secret và thử lại.";
  }

  const baseUrl = (env.OPENAI_BASE_URL || "https://api.openai.com/v1").replace(/\/+$/, "");
  const endpoint = `${baseUrl}/chat/completions`;
  const model = env.AI_MODEL || "gpt-4o-mini";

  const resp = await fetch(endpoint, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${env.OPENAI_API_KEY}`,
    },
    body: JSON.stringify({
      model,
      messages: [
        {
          role: "system",
          content:
            "You are PicoClaw running on Cloudflare Worker. Keep responses concise and helpful.",
        },
        { role: "user", content: userInput },
      ],
      temperature: 0.4,
    }),
  });

  if (!resp.ok) {
    const textBody = await resp.text();
    throw new Error(`ai completion failed: ${resp.status} ${textBody}`);
  }

  const data = (await resp.json()) as {
    choices?: Array<{ message?: { content?: string } }>;
  };
  const answer = data.choices?.[0]?.message?.content?.trim();
  return answer || "Mình chưa tạo được câu trả lời, bạn thử lại giúp nhé.";
}

async function getRecentContext(env: Env, chatId: number): Promise<string> {
  if (!env.CHAT_STATE) {
    return "";
  }

  const key = `chat:${chatId}:last`;
  const cached = await env.CHAT_STATE.get(key);
  return cached || "";
}

async function saveRecentContext(env: Env, chatId: number, userInput: string, answer: string): Promise<void> {
  if (!env.CHAT_STATE) {
    return;
  }

  const key = `chat:${chatId}:last`;
  const payload = JSON.stringify({
    user: userInput,
    assistant: answer,
    updatedAt: new Date().toISOString(),
  });
  await env.CHAT_STATE.put(key, payload, { expirationTtl: 60 * 60 * 24 });
}

async function handleTelegramWebhook(request: Request, env: Env, requestId: string): Promise<Response> {
  if (!env.TELEGRAM_BOT_TOKEN) {
    return jsonResponse(500, { ok: false, error: "missing TELEGRAM_BOT_TOKEN", requestId });
  }

  if (env.TELEGRAM_WEBHOOK_SECRET) {
    const secretHeader = request.headers.get("x-telegram-bot-api-secret-token") || "";
    if (secretHeader !== env.TELEGRAM_WEBHOOK_SECRET) {
      console.log(
        JSON.stringify({
          level: "warn",
          requestId,
          event: "telegram_webhook_secret_mismatch",
        })
      );
      return jsonResponse(401, { ok: false, error: "unauthorized", requestId });
    }
  }

  const update = (await request.json()) as TelegramUpdate;
  const message = update.message;
  const chatId = message?.chat?.id;
  const fromId = message?.from?.id;
  const userText = (message?.text || "").trim();

  console.log(
    JSON.stringify({
      level: "info",
      requestId,
      event: "telegram_update_received",
      updateId: update.update_id,
      fromId,
      chatId,
      hasText: Boolean(userText),
    })
  );

  if (!chatId || !fromId || !userText) {
    return jsonResponse(200, { ok: true, skipped: "non-text-or-missing-fields", requestId });
  }

  const allowedUsers = extractAllowedUsers(env.TELEGRAM_ALLOW_FROM || "");
  if (allowedUsers.size > 0 && !allowedUsers.has(String(fromId))) {
    console.log(
      JSON.stringify({
        level: "warn",
        requestId,
        event: "telegram_user_blocked",
        fromId,
      })
    );
    await sendTelegramMessage(env, chatId, "Bạn chưa có quyền sử dụng bot này.");
    return jsonResponse(200, { ok: true, blocked: true, requestId });
  }

  if (userText === "/start") {
    await sendTelegramMessage(
      env,
      chatId,
      "✅ PicoClaw Worker đã sẵn sàng. Gửi câu hỏi bất kỳ để bắt đầu."
    );
    return jsonResponse(200, { ok: true, handled: "start", requestId });
  }

  try {
    const recentContext = await getRecentContext(env, chatId);
    const prompt = recentContext
      ? `Recent context: ${recentContext}\n\nNew user message: ${userText}`
      : userText;
    const answer = await completeWithAI(env, prompt);
    await sendTelegramMessage(env, chatId, answer);
    await saveRecentContext(env, chatId, userText, answer);
    console.log(
      JSON.stringify({
        level: "info",
        requestId,
        event: "telegram_reply_sent",
        fromId,
        chatState: env.CHAT_STATE ? "updated" : "disabled",
      })
    );
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    console.log(
      JSON.stringify({
        level: "error",
        requestId,
        event: "telegram_reply_failed",
        error: reason,
      })
    );
    await sendTelegramMessage(
      env,
      chatId,
      "⚠️ Mình gặp lỗi khi xử lý yêu cầu. Kiểm tra OPENAI_API_KEY / model / provider rồi thử lại nhé."
    );
  }

  return jsonResponse(200, { ok: true, requestId });
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const requestId = buildRequestId(request);
    const url = new URL(request.url);

    if (request.method === "GET" && url.pathname === "/health") {
      return jsonResponse(200, {
        ok: true,
        service: env.APP_NAME || "picoclaw",
        env: env.ENVIRONMENT || "preview",
        timestamp: new Date().toISOString(),
        requestId,
      });
    }

    if (request.method === "POST" && url.pathname === "/telegram/webhook") {
      return handleTelegramWebhook(request, env, requestId);
    }

    if (request.method === "GET" && url.pathname === "/") {
      return new Response(
        `🦞 ${env.APP_NAME || "picoclaw"} worker is live on Cloudflare (${env.ENVIRONMENT || "preview"}).`,
        { headers: { "content-type": "text/plain; charset=utf-8" } }
      );
    }

    return jsonResponse(404, {
      ok: false,
      error: "not_found",
      requestId,
      path: url.pathname,
    });
  },
};
