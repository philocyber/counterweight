const MAX_RESPONSE_BYTES = 2 * 1024 * 1024;

export function configFromEnv(env = process.env) {
  const baseURL = new URL(env.COUNTERWEIGHT_URL || "http://127.0.0.1:8787");
  const loopback = ["localhost", "127.0.0.1", "[::1]"].includes(baseURL.hostname);
  if ((baseURL.protocol !== "https:" && !(baseURL.protocol === "http:" && loopback)) ||
      baseURL.username || baseURL.password || baseURL.search || baseURL.hash || baseURL.pathname !== "/") {
    throw new Error("COUNTERWEIGHT_URL must be an HTTPS origin or a loopback HTTP origin, without credentials or a path.");
  }
  const language = env.COUNTERWEIGHT_LANGUAGE || "en";
  if (language !== "en") throw new Error("COUNTERWEIGHT_LANGUAGE must be en.");
  const timeoutMs = Number(env.COUNTERWEIGHT_TIMEOUT_MS || 30000);
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 120000) {
    throw new Error("COUNTERWEIGHT_TIMEOUT_MS must be an integer from 1 to 120000.");
  }
  const token = env.COUNTERWEIGHT_TOKEN || "";
  if (/[\r\n]/.test(token)) throw new Error("COUNTERWEIGHT_TOKEN must not contain line breaks.");
  if (env.COUNTERWEIGHT_ALLOW_WRITES && !["true", "false"].includes(env.COUNTERWEIGHT_ALLOW_WRITES)) {
    throw new Error("COUNTERWEIGHT_ALLOW_WRITES must be true or false.");
  }
  return { baseURL, language, timeoutMs, token, password: env.COUNTERWEIGHT_PASSWORD || "",
    allowWrites: env.COUNTERWEIGHT_ALLOW_WRITES === "true" };
}

export class CounterweightClient {
  constructor(config = configFromEnv()) {
    this.config = config;
    this.token = config.token;
    this.login = null;
  }

  redact(message) {
    for (const secret of [this.token, this.config.token, this.config.password]) {
      if (secret) message = message.replaceAll(secret, "[redacted]");
    }
    return message;
  }

  async authenticate(signal) {
    if (this.token) return;
    if (!this.config.password) throw new Error("Set COUNTERWEIGHT_TOKEN or COUNTERWEIGHT_PASSWORD to authenticate.");
    // Share a single login when an agent calls multiple read tools concurrently.
    if (!this.login) {
      this.login = this.request("/api/auth/login", {
        method: "POST", body: { username: "COUNTERWEIGHT", password: this.config.password }, auth: false, signal,
      }).then((result) => {
        if (typeof result.token !== "string" || !result.token) throw new Error("Counterweight login did not return a token.");
        this.token = result.token;
      }).finally(() => { this.login = null; });
    }
    await this.login;
  }

  async request(path, { method = "GET", body, query = {}, auth = true, signal, timeoutMs = this.config.timeoutMs } = {}) {
    if (!path.startsWith("/api/")) throw new Error("Only Counterweight API requests are supported.");
    if (auth) await this.authenticate(signal);
    const url = new URL(path, this.config.baseURL);
    if (url.origin !== this.config.baseURL.origin) throw new Error("API requests must stay on the configured origin.");
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined) url.searchParams.set(key, String(value));
    }
    const timeout = AbortSignal.timeout(timeoutMs);
    const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
    try {
      const response = await fetch(url, {
        method, redirect: "error", signal: combined,
        headers: { "Accept": "application/json", "Accept-Language": this.config.language,
          ...(auth ? { "Authorization": `Bearer ${this.token}` } : {}),
          ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
        ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
      });
      const reader = response.body.getReader();
      const chunks = [];
      let length = 0;
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          length += value.byteLength;
          if (length > MAX_RESPONSE_BYTES) throw new Error("Counterweight response exceeds 2 MiB; narrow the query.");
          chunks.push(Buffer.from(value));
        }
      } finally { await reader.cancel(); }
      let result;
      try { result = JSON.parse(Buffer.concat(chunks).toString("utf8")); }
      catch { throw new Error(`Counterweight returned a non-JSON response (HTTP ${response.status}).`); }
      if (!response.ok) {
        const hint = response.status === 401 ? " Sign in again or replace the expired token." : "";
        const detail = typeof result?.error === "string" ? this.redact(result.error).slice(0, 500) : "Request failed";
        throw new Error(`Counterweight HTTP ${response.status}: ${detail}.${hint}`);
      }
      return result;
    } catch (error) {
      if (combined.aborted) throw new Error(signal?.aborted ? "Counterweight request cancelled." : "Counterweight request timed out; check task status before retrying a write.");
      throw new Error(this.redact(error.message));
    }
  }
}
