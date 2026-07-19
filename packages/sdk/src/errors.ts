// SPDX-License-Identifier: FSL-1.1-Apache-2.0

/**
 * The API's error body shape ({ error, message }), plus the extra fields a 402
 * quota response carries.
 */
export interface ApiErrorBody {
  error?: string;
  message?: string;
  // 402 quota fields (see quota.ExceededError on the server).
  limit?: number;
  current?: number;
  max?: number;
  plan_tier?: string;
  next_tier?: string;
  upgrade_url?: string;
  sales_url?: string;
}

/** Base class for every error the SDK throws for a non-2xx API response. */
export class DropwayError extends Error {
  /** HTTP status code. */
  readonly status: number;
  /** The parsed error body, when the response had a JSON one. */
  readonly body: ApiErrorBody | null;

  constructor(status: number, message: string, body: ApiErrorBody | null) {
    super(message);
    this.name = "DropwayError";
    this.status = status;
    this.body = body;
  }
}

/** 401 — the API key is missing, malformed, revoked, expired, or its org disabled keys. */
export class AuthError extends DropwayError {
  constructor(message: string, body: ApiErrorBody | null) {
    super(401, message, body);
    this.name = "AuthError";
  }
}

/**
 * 403 — the action is forbidden. For an API key this is usually the member-level
 * role ceiling (admin actions require an interactive login); `interactiveRequired`
 * flags that case so callers can print a useful hint.
 */
export class ForbiddenError extends DropwayError {
  readonly interactiveRequired: boolean;
  constructor(message: string, body: ApiErrorBody | null) {
    super(403, message, body);
    this.name = "ForbiddenError";
    this.interactiveRequired = /interactive login|member-level/i.test(
      body?.message ?? message,
    );
  }
}

/** 404 — the target resource doesn't exist (or isn't visible to this org). */
export class NotFoundError extends DropwayError {
  constructor(message: string, body: ApiErrorBody | null) {
    super(404, message, body);
    this.name = "NotFoundError";
  }
}

/**
 * 402 — a plan quota was crossed (e.g. the site cap). Carries the parsed upgrade
 * fields so a CLI can print "site cap reached (10/10 on free), upgrade at …".
 */
export class QuotaExceededError extends DropwayError {
  readonly limit?: number;
  readonly current?: number;
  readonly max?: number;
  readonly planTier?: string;
  readonly nextTier?: string;
  readonly upgradeUrl?: string;

  constructor(message: string, body: ApiErrorBody | null) {
    super(402, message, body);
    this.name = "QuotaExceededError";
    this.limit = body?.limit;
    this.current = body?.current;
    this.max = body?.max;
    this.planTier = body?.plan_tier;
    this.nextTier = body?.next_tier;
    this.upgradeUrl = body?.upgrade_url;
  }
}

/**
 * 429 — the request was rate-limited. `retryAfterSeconds` is the server's
 * Retry-After (seconds) when present, so callers can back off.
 */
export class RateLimitError extends DropwayError {
  readonly retryAfterSeconds?: number;
  constructor(
    message: string,
    body: ApiErrorBody | null,
    retryAfterSeconds?: number,
  ) {
    super(429, message, body);
    this.name = "RateLimitError";
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

/**
 * Construct the most specific DropwayError subclass for a response. `retryAfter`
 * is the parsed Retry-After header (seconds), used for 429s.
 */
export function errorForResponse(
  status: number,
  body: ApiErrorBody | null,
  retryAfter?: number,
): DropwayError {
  const msg = body?.message || body?.error || `Dropway API error ${status}`;
  switch (status) {
    case 401:
      return new AuthError(msg, body);
    case 402:
      return new QuotaExceededError(msg, body);
    case 403:
      return new ForbiddenError(msg, body);
    case 404:
      return new NotFoundError(msg, body);
    case 429:
      return new RateLimitError(msg, body, retryAfter);
    default:
      return new DropwayError(status, msg, body);
  }
}
