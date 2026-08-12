// Thin client for the coordinator's HTTP API (see CLAUDE.md's "Coordinator
// API" command reference). No data-fetching library — the API surface is
// small enough that plain fetch + a few typed wrappers is proportionate.

const COORDINATOR_URL =
  process.env.NEXT_PUBLIC_COORDINATOR_URL || "http://localhost:8080";

const TOKEN_KEY = "sentryload_token";

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

export function loginUrl(): string {
  return `${COORDINATOR_URL}/auth/github/login`;
}

export class ApiError extends Error {
  status: number;
  retryAfterSeconds?: number;

  constructor(status: number, message: string, retryAfterSeconds?: number) {
    super(message);
    this.status = status;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = getToken();
  const res = await fetch(`${COORDINATOR_URL}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      // The coordinator is tunneled through ngrok's free tier, which
      // intercepts every request (including background fetches, not just
      // page navigations) with an HTML "abuse interstitial" unless this
      // header is present. Harmless no-op against any other host.
      "ngrok-skip-browser-warning": "true",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...options.headers,
    },
  });

  if (!res.ok) {
    // A 401 here means the coordinator doesn't recognize this token
    // anymore — sessions are in-memory (see CLAUDE.md), so any coordinator
    // restart invalidates every session at once. Rather than let stale
    // pages sit there erroring on every action, drop the dead token and
    // send the user back to log in again.
    if (res.status === 401) {
      clearToken();
      // This is plain module code outside the component tree, so
      // useRouter() isn't available here — and a full reload is the right
      // call anyway, to guarantee no stale authenticated state lingers
      // client-side after the session's been invalidated.
      // eslint-disable-next-line @next/next/no-location-assign-relative-destination
      if (typeof window !== "undefined") window.location.assign("/");
    }
    const body = await res.json().catch(() => ({}) as { error?: string });
    const retryAfter = res.headers.get("Retry-After");
    throw new ApiError(
      res.status,
      body.error || `request failed: ${res.status}`,
      retryAfter ? Number(retryAfter) : undefined,
    );
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export interface SubJobSnapshot {
  job_id: string;
  requests: number;
  errors: number;
  rps: number;
  p50_ms: string;
  p95_ms: string;
  p99_ms: string;
  done: boolean;
  circuit_broken: boolean;
}

export interface TestSnapshot {
  test_id: string;
  url: string;
  done: boolean;
  circuit_broken: boolean;
  total_requests: number;
  total_errors: number;
  combined_rps: number;
  sub_jobs: SubJobSnapshot[] | null;
  // Only set on snapshots that came from test history (list/trend), not
  // a live in-flight test.
  finished_at?: string;
  // User-set display name, empty until set via setTestLabel — personal
  // history organization, not shared/team labeling.
  label?: string;
}

export function listTests(): Promise<TestSnapshot[]> {
  return request<TestSnapshot[]>("/tests");
}

export function getTest(id: string): Promise<TestSnapshot> {
  return request<TestSnapshot>(`/tests/${id}`);
}

// getTestTrend returns the caller's past finished tests against url,
// oldest first — the series behind the per-target trend view.
export function getTestTrend(url: string): Promise<TestSnapshot[]> {
  return request<TestSnapshot[]>(`/tests/trend?url=${encodeURIComponent(url)}`);
}

export interface CreateTestInput {
  url: string;
  vus: number;
  duration_seconds: number;
  ramp_pattern: "steady" | "ramp";
  worker_count?: number;
}

export function createTest(
  input: CreateTestInput,
): Promise<{ test_id: string; sub_job_ids: string[]; warning?: string }> {
  return request("/tests", { method: "POST", body: JSON.stringify(input) });
}

export interface DomainChallenge {
  domain: string;
  token: string;
  dns_record: string;
  dns_value: string;
  well_known_url: string;
}

export function requestDomainChallenge(domain: string): Promise<DomainChallenge> {
  return request("/domains", {
    method: "POST",
    body: JSON.stringify({ domain }),
  });
}

export function verifyDomain(
  domain: string,
  method: "dns" | "well-known",
): Promise<{ domain: string; verified: boolean }> {
  return request(`/domains/${encodeURIComponent(domain)}/verify`, {
    method: "POST",
    body: JSON.stringify({ method }),
  });
}

// testLiveWsUrl builds the GET /tests/{id}/live URL. Unlike every other
// request here, the token has to travel as a query param, not the
// Authorization header — browsers don't let a WebSocket handshake set
// custom headers (see coordinator/live.go for the same tradeoff noted
// server-side).
export function testLiveWsUrl(id: string): string {
  const token = getToken() ?? "";
  const wsBase = COORDINATOR_URL.replace(/^http/, "ws");
  return `${wsBase}/tests/${id}/live?token=${encodeURIComponent(token)}`;
}

export function shareTest(
  id: string,
): Promise<{ share_token: string; share_url: string }> {
  return request(`/tests/${id}/share`, { method: "POST" });
}

// badgeUrl builds the embeddable SVG badge URL for a share token — served
// straight from the coordinator (not the dashboard), since it's meant to
// be dropped into a README's <img src="..."> with no dashboard involved
// at all (M15).
export function badgeUrl(shareToken: string): string {
  return `${COORDINATOR_URL}/badge/${shareToken}`;
}

// getPublicReport hits GET /reports/{token} directly rather than through
// request() — that helper attaches a bearer token when one exists in
// localStorage, but a shared report is meant to be viewable by someone
// with no account at all, so this deliberately never sends one.
export async function getPublicReport(token: string): Promise<TestSnapshot> {
  const res = await fetch(`${COORDINATOR_URL}/reports/${token}`, {
    headers: { "ngrok-skip-browser-warning": "true" },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}) as { error?: string });
    throw new ApiError(res.status, body.error || `request failed: ${res.status}`);
  }
  return res.json();
}

export interface WebhookSettings {
  webhook_url: string;
}

// getWebhookSettings/setWebhookSettings manage the Discord/Slack chat
// webhook the coordinator notifies whenever one of the caller's tests
// finishes.
export function getWebhookSettings(): Promise<WebhookSettings> {
  return request<WebhookSettings>("/me/webhook");
}

export function setWebhookSettings(webhookUrl: string): Promise<WebhookSettings> {
  return request<WebhookSettings>("/me/webhook", {
    method: "PUT",
    body: JSON.stringify({ webhook_url: webhookUrl }),
  });
}

// deleteTest permanently removes a finished test from the caller's own
// history. Personal cleanup only — there's no shared/team history to
// delete from.
export function deleteTest(id: string): Promise<void> {
  return request<void>(`/tests/${id}`, { method: "DELETE" });
}

// setTestLabel sets or clears a test's display name — an empty string
// clears it back to just showing the URL.
export function setTestLabel(id: string, label: string): Promise<{ label: string }> {
  return request(`/tests/${id}/label`, {
    method: "PUT",
    body: JSON.stringify({ label }),
  });
}
