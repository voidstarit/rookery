export class APIError extends Error {
  status: number | undefined;

  constructor(message: string, status?: number) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

// Retry the same request after these delays before giving up. Together with the
// initial attempt this means at most five requests are sent over roughly 17s.
const RETRY_DELAYS_MS = [1000, 2000, 4000, 10000] as const;
const MAX_ATTEMPTS = RETRY_DELAYS_MS.length + 1;

// Statuses where retrying is safe for every method. 429 is our rate limiter and
// 503 means a prerequisite is unavailable; the server rejected before writing.
const ALWAYS_RETRYABLE_STATUS = new Set([429, 503]);
// Statuses where the server may have failed after processing for idempotent
// requests. Mutations only retry the two statuses above to avoid duplicates.
const IDEMPOTENT_RETRYABLE_STATUS = new Set([408, 425, 429, 500, 502, 503, 504]);

const IDEMPOTENT_METHODS = new Set(["GET", "HEAD", "PUT", "DELETE", "OPTIONS"]);

// GET requests in flight are shared so several tabs and polling loops asking for
// the same resource do not stack requests (or retry chains) on the server.
const inFlight = new Map<string, Promise<unknown>>();

function methodOf(options: RequestInit): string {
  return (options.method || "GET").toUpperCase();
}

function abortError(): Error {
  return typeof DOMException === "function"
    ? new DOMException("The request was aborted", "AbortError")
    : Object.assign(new Error("The request was aborted"), { name: "AbortError" });
}

function wait(ms: number, signal?: AbortSignal | null): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(abortError());
      return;
    }
    const timer = window.setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      window.clearTimeout(timer);
      reject(abortError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function shouldRetry(method: string, error: APIError, signal?: AbortSignal | null): boolean {
  if (signal?.aborted) {
    return false;
  }
  if (error.status === undefined) {
    // A network failure may mean the request never arrived, so only idempotent
    // requests are retried; blindly repeating a create could make duplicates.
    return IDEMPOTENT_METHODS.has(method);
  }
  if (ALWAYS_RETRYABLE_STATUS.has(error.status)) {
    return true;
  }
  return IDEMPOTENT_METHODS.has(method) && IDEMPOTENT_RETRYABLE_STATUS.has(error.status);
}

function exhaustedError(method: string, path: string, last: APIError): APIError {
  const detail = last.status === undefined
    ? last.message || "network error"
    : `${last.message || "request failed"} (HTTP ${last.status})`;
  return new APIError(
    `${method} ${path} failed after ${MAX_ATTEMPTS} attempts: ${detail}`,
    last.status
  );
}

async function requestOnce<T>(path: string, options: RequestInit): Promise<T> {
  const headers = new Headers(options.headers || {});
  if (options.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }

  let response: Response;
  try {
    response = await fetch(path, { ...options, headers });
  } catch (err) {
    if (err instanceof Error && err.name === "AbortError") {
      throw err;
    }
    throw new APIError(err instanceof Error ? err.message : String(err));
  }

  let data: unknown = null;
  if (response.status !== 204) {
    const text = await response.text();
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = null;
      }
    }
  }

  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    if (data && typeof data === "object") {
      if ("error" in data && typeof data.error === "string") {
        message = data.error;
      } else if ("message" in data && typeof data.message === "string") {
        message = data.message;
      }
    }
    throw new APIError(message, response.status);
  }

  return data as T;
}

async function requestWithRetry<T>(path: string, options: RequestInit): Promise<T> {
  const method = methodOf(options);
  let last: APIError | undefined;

  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt += 1) {
    try {
      return await requestOnce<T>(path, options);
    } catch (err) {
      if (!(err instanceof APIError)) {
        throw err;
      }
      last = err;
      if (!shouldRetry(method, err, options.signal)) {
        throw err;
      }
      if (attempt >= MAX_ATTEMPTS) {
        throw exhaustedError(method, path, err);
      }
      const delay = RETRY_DELAYS_MS[attempt - 1];
      console.warn(
        `${method} ${path} failed (attempt ${attempt}/${MAX_ATTEMPTS}); retrying in ${delay / 1000}s: ${err.message}`
      );
      await wait(delay, options.signal);
    }
  }

  throw exhaustedError(method, path, last ?? new APIError("request failed"));
}

export function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const method = methodOf(options);
  if (method !== "GET" || options.signal) {
    return requestWithRetry<T>(path, options);
  }

  const key = `GET ${path}`;
  const existing = inFlight.get(key);
  if (existing) {
    return existing as Promise<T>;
  }

  const promise = requestWithRetry<T>(path, options).finally(() => {
    if (inFlight.get(key) === promise) {
      inFlight.delete(key);
    }
  });
  inFlight.set(key, promise);
  return promise;
}
