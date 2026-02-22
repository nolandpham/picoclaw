export interface Env {
  APP_NAME: string;
  ENVIRONMENT: string;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/health") {
      return new Response(
        JSON.stringify({
          ok: true,
          service: env.APP_NAME || "picoclaw",
          env: env.ENVIRONMENT || "preview",
          timestamp: new Date().toISOString(),
        }),
        { headers: { "content-type": "application/json; charset=utf-8" } }
      );
    }

    return new Response(
      `🦞 ${env.APP_NAME || "picoclaw"} worker is live on Cloudflare (${env.ENVIRONMENT || "preview"}).`,
      { headers: { "content-type": "text/plain; charset=utf-8" } }
    );
  },
};
