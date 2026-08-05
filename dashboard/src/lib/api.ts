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
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...options.headers,
    },
  });

  if (!res.ok) {
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
}

export function listTests(): Promise<TestSnapshot[]> {
  return request<TestSnapshot[]>("/tests");
}

export function getTest(id: string): Promise<TestSnapshot> {
  return request<TestSnapshot>(`/tests/${id}`);
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
): Promise<{ test_id: string; sub_job_ids: string[] }> {
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
