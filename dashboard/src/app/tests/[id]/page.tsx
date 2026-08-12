"use client";

import { use, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ApiError,
  TestSnapshot,
  badgeUrl,
  getTest,
  getTestTrend,
  getToken,
  shareTest,
  testLiveWsUrl,
} from "@/lib/api";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import LineChart from "@/components/LineChart";
import BarChart from "@/components/BarChart";
import AnimatedNumber from "@/components/AnimatedNumber";
import StatusDot, { testStatus } from "@/components/StatusDot";
import InfoTooltip from "@/components/InfoTooltip";

// Cumulative totals climb every tick regardless of what's actually
// happening right now, which buries spikes — a jump from 0 to 20 errors
// out of 1000 requests barely bends the line. Diffing consecutive
// snapshots turns them back into "what happened in this ~1s tick", which
// is what actually shows a spike.
function toPerTickDeltas(cumulative: number[]): number[] {
  return cumulative.map((v, i) => (i === 0 ? v : Math.max(v - cumulative[i - 1], 0)));
}

// Smaller, letter-spaced, muted column labels read as a data table built
// with intent — the shadcn default (same size/weight as the body text)
// doesn't visually separate "this is a header" from "this is a value."
const TH_LABEL = "h-8 text-[11px] font-medium tracking-wide text-muted-foreground uppercase";

export default function TestPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const [snap, setSnap] = useState<TestSnapshot | null>(null);
  const [connectionState, setConnectionState] = useState<
    "connecting" | "live" | "closed"
  >("connecting");
  const [requestsHistory, setRequestsHistory] = useState<number[]>([]);
  const [errorsHistory, setErrorsHistory] = useState<number[]>([]);
  const [rpsHistory, setRpsHistory] = useState<number[]>([]);
  const [shareUrl, setShareUrl] = useState<string | null>(null);
  const [shareToken, setShareToken] = useState<string | null>(null);
  const [shareError, setShareError] = useState<string | null>(null);
  const [sharing, setSharing] = useState(false);
  const [copied, setCopied] = useState(false);
  const [embedCopied, setEmbedCopied] = useState(false);
  // Read directly off window.location rather than next/navigation's
  // useSearchParams — that hook needs a Suspense boundary the moment it's
  // used, and a one-time read on mount doesn't need it.
  const [capacityWarning, setCapacityWarning] = useState<string | null>(null);
  const [trend, setTrend] = useState<TestSnapshot[] | null>(null);

  useEffect(() => {
    // window doesn't exist during server rendering, so this can only be
    // read after mount — same reasoning as Nav.tsx's localStorage read.
    const warning = new URLSearchParams(window.location.search).get("warning");
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (warning) setCapacityWarning(warning);
  }, []);

  useEffect(() => {
    if (!getToken()) {
      router.replace("/");
      return;
    }

    let cancelled = false;
    const socket = new WebSocket(testLiveWsUrl(id));

    socket.onopen = () => {
      if (!cancelled) setConnectionState("live");
    };
    socket.onmessage = (event) => {
      if (cancelled) return;
      const next = JSON.parse(event.data) as TestSnapshot;
      setSnap(next);
      setRequestsHistory((h) => [...h, next.total_requests]);
      setErrorsHistory((h) => [...h, next.total_errors]);
      setRpsHistory((h) => [...h, next.combined_rps]);
    };
    socket.onclose = () => {
      if (cancelled) return;
      setConnectionState("closed");
      // The WebSocket closes right after the final done=true message, so
      // this is usually a no-op — but it's a safety net if the socket
      // drops for any other reason (e.g. a network blip) without one.
      getTest(id)
        .then((s) => !cancelled && setSnap(s))
        .catch(() => {});
    };

    return () => {
      cancelled = true;
      socket.close();
    };
  }, [id, router]);

  useEffect(() => {
    if (!snap?.done) return;
    let cancelled = false;
    // Requires Postgres (test history) — 501s harmlessly if it's not
    // configured on this coordinator, same as the trend endpoint itself
    // reports. No trend section shows up in that case, which is the
    // right degradation: nothing to compare against yet either way.
    getTestTrend(snap.url)
      .then((series) => !cancelled && setTrend(series))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [snap?.done, snap?.url]);

  async function handleShare() {
    setShareError(null);
    setSharing(true);
    try {
      const { share_url, share_token } = await shareTest(id);
      setShareUrl(share_url);
      setShareToken(share_token);
    } catch (err) {
      setShareError(err instanceof ApiError ? err.message : "Something went wrong.");
    } finally {
      setSharing(false);
    }
  }

  async function copyShareUrl() {
    if (!shareUrl) return;
    await navigator.clipboard.writeText(shareUrl);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  async function copyEmbedCode() {
    if (!shareToken || !shareUrl) return;
    const markdown = `[![Load tested by Sentry Load](${badgeUrl(shareToken)})](${shareUrl})`;
    await navigator.clipboard.writeText(markdown);
    setEmbedCopied(true);
    setTimeout(() => setEmbedCopied(false), 2000);
  }

  if (!snap) {
    return (
      <main className="mx-auto w-full max-w-3xl flex-1 px-6 py-10">
        <p className="text-muted-foreground">
          {connectionState === "connecting" ? "Connecting…" : "Loading…"}
        </p>
      </main>
    );
  }

  const errorRate =
    snap.total_requests > 0
      ? (snap.total_errors / snap.total_requests) * 100
      : 0;

  const requestsPerTick = toPerTickDeltas(requestsHistory);
  const errorsPerTick = toPerTickDeltas(errorsHistory);
  const errorRatePerTick = requestsPerTick.map((req, i) =>
    req > 0 ? (errorsPerTick[i] / req) * 100 : 0,
  );

  return (
    <main className="mx-auto w-full max-w-3xl flex-1 px-6 py-10">
      <Link
        href="/tests"
        className="group -ml-2 mb-4 inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth={1.5}
          className="transition-transform duration-150 group-hover:-translate-x-0.5"
        >
          <path d="M10 12.5 5.5 8 10 3.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        All tests
      </Link>

      <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="break-all text-2xl font-semibold tracking-tight">
            {snap.url}
          </h1>
          <p className="mt-1 font-mono text-xs text-muted-foreground">
            {snap.test_id}
          </p>
        </div>
        <StatusBadge done={snap.done} circuitBroken={snap.circuit_broken} />
      </div>

      {capacityWarning && (
        <div className="mb-6 flex items-start justify-between gap-3 rounded-lg bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
          <p>{capacityWarning}</p>
          <button
            onClick={() => setCapacityWarning(null)}
            className="cursor-pointer text-xs underline"
          >
            Dismiss
          </button>
        </div>
      )}

      {snap.circuit_broken && (
        <p className="mb-6 rounded-lg bg-amber-500/10 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
          This test stopped early. Your target&apos;s error rate spiked
          past 50%, so we cut it short instead of grinding through the full
          duration against something that was clearly struggling
        </p>
      )}

      <div className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Total requests" value={snap.total_requests} showDelta />
        <Stat label="Total errors" value={snap.total_errors} showDelta />
        <Stat
          label="Error rate"
          value={errorRate}
          format={(v) => `${v.toFixed(1)}%`}
          tone={errorRate > 5 ? "bad" : "good"}
        />
        <Stat
          label="Combined RPS"
          value={snap.combined_rps}
          format={(v) => v.toFixed(1)}
        />
      </div>

      <div className="mb-8 grid grid-cols-1 gap-6 sm:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1.5">
              Requests per tick
              <InfoTooltip>
                How many requests landed in each ~1s window. Shows real
                moment-to-moment throughput, not a running total
              </InfoTooltip>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <BarChart points={requestsPerTick} formatValue={(v) => v.toFixed(0)} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1.5">
              Error rate per tick
              <InfoTooltip>
                Errors as a percent of that tick&apos;s requests. Spikes
                show up immediately instead of being smoothed into the
                running total
              </InfoTooltip>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <LineChart
              points={errorRatePerTick}
              color="var(--destructive)"
              formatValue={(v) => `${v.toFixed(1)}%`}
            />
          </CardContent>
        </Card>
      </div>

      <div className="mb-8">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1.5">
              Combined RPS over time
              <InfoTooltip>
                The coordinator&apos;s own running-average RPS. Useful for
                comparing against the per-tick view above
              </InfoTooltip>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <LineChart points={rpsHistory} formatValue={(v) => v.toFixed(1)} />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5">
            Per worker
            <InfoTooltip>
              Each worker&apos;s latency percentiles are shown separately.
              Merging percentiles across independent machines isn&apos;t
              statistically valid without the raw samples
            </InfoTooltip>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className={TH_LABEL}>Worker</TableHead>
                <TableHead className={`${TH_LABEL} text-right`}>Requests</TableHead>
                <TableHead className={`${TH_LABEL} text-right`}>Errors</TableHead>
                <TableHead className={`${TH_LABEL} text-right`}>RPS</TableHead>
                <TableHead className={`${TH_LABEL} text-right`}>p50</TableHead>
                <TableHead className={`${TH_LABEL} text-right`}>p95</TableHead>
                <TableHead className={`${TH_LABEL} text-right`}>p99</TableHead>
                <TableHead className={TH_LABEL}>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(snap.sub_jobs ?? []).map((sj) => (
                <TableRow key={sj.job_id}>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {sj.job_id.slice(0, 8)}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {sj.requests.toLocaleString()}
                  </TableCell>
                  <TableCell
                    className={`text-right tabular-nums ${sj.errors > 0 ? "text-destructive" : ""}`}
                  >
                    {sj.errors.toLocaleString()}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{sj.rps.toFixed(1)}</TableCell>
                  <TableCell className="text-right tabular-nums text-muted-foreground">
                    {sj.p50_ms}ms
                  </TableCell>
                  <TableCell className="text-right tabular-nums text-muted-foreground">
                    {sj.p95_ms}ms
                  </TableCell>
                  <TableCell className="text-right tabular-nums text-muted-foreground">
                    {sj.p99_ms}ms
                  </TableCell>
                  <TableCell>
                    <StatusDot status={testStatus(sj.done, sj.circuit_broken)} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {trend && trend.length >= 2 && (
        <div className="mt-8">
          <h2 className="mb-1 text-lg font-semibold tracking-tight">
            Trend for this target
          </h2>
          <p className="mb-4 text-sm text-muted-foreground">
            Your last {trend.length} runs against this exact URL, oldest to
            newest. A rising RPS line or a falling error-rate line means
            it&apos;s actually getting better, not just this one run
          </p>
          <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-1.5">
                  Combined RPS across runs
                  <InfoTooltip>
                    Each point is one finished test&apos;s final combined RPS
                  </InfoTooltip>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <LineChart
                  points={trend.map((t) => t.combined_rps)}
                  formatValue={(v) => v.toFixed(1)}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-1.5">
                  Error rate across runs
                  <InfoTooltip>
                    Each point is one finished test&apos;s overall error rate
                  </InfoTooltip>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <LineChart
                  points={trend.map((t) =>
                    t.total_requests > 0
                      ? (t.total_errors / t.total_requests) * 100
                      : 0,
                  )}
                  color="var(--destructive)"
                  formatValue={(v) => `${v.toFixed(1)}%`}
                />
              </CardContent>
            </Card>
          </div>
        </div>
      )}

      {snap.done && (
        <div className="mt-6 space-y-3 rounded-xl border bg-muted/50 p-4">
          <div className="flex items-center justify-between gap-3">
            <p className="text-sm text-muted-foreground">
              This test has finished running.
            </p>
            <div className="flex gap-2">
              <Button variant="outline" onClick={handleShare} disabled={sharing}>
                {shareUrl ? "Refresh link" : sharing ? "Creating link…" : "Share"}
              </Button>
              <Button nativeButton={false} render={<Link href="/tests" />}>
                Run another test
              </Button>
            </div>
          </div>

          {shareError && (
            <p className="text-sm text-destructive">{shareError}</p>
          )}

          {shareUrl && (
            <div className="flex gap-2">
              <Input readOnly value={shareUrl} className="font-mono text-xs" />
              <Button variant="outline" onClick={copyShareUrl}>
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
          )}

          {shareToken && (
            <div className="flex items-center justify-between gap-3 border-t pt-3">
              <div className="flex items-center gap-2">
                {/* A dynamically-generated SVG from the coordinator, not a
                    static local asset — next/image's optimizer doesn't apply
                    here, a plain img is the right tool. */}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={badgeUrl(shareToken)} alt="Load tested by Sentry Load" />
                <span className="text-xs text-muted-foreground">
                  Embed this in your README
                </span>
              </div>
              <Button variant="outline" size="sm" onClick={copyEmbedCode}>
                {embedCopied ? "Copied" : "Copy embed code"}
              </Button>
            </div>
          )}
        </div>
      )}
    </main>
  );
}

function Stat({
  label,
  value,
  format = (v: number) => Math.round(v).toLocaleString(),
  tone,
  showDelta,
}: {
  label: string;
  value: number;
  format?: (v: number) => string;
  tone?: "good" | "bad";
  showDelta?: boolean;
}) {
  const prevRef = useRef(value);
  const [delta, setDelta] = useState<number | null>(null);

  useEffect(() => {
    if (!showDelta) {
      prevRef.current = value;
      return;
    }
    const diff = value - prevRef.current;
    prevRef.current = value;
    if (diff <= 0) return;

    setDelta(diff);
    const timer = setTimeout(() => setDelta(null), 1500);
    return () => clearTimeout(timer);
  }, [value, showDelta]);

  return (
    <Card>
      <CardContent>
        <p className="text-xs text-muted-foreground">{label}</p>
        <div className="flex items-baseline gap-2">
          <p
            className={`text-2xl font-semibold tracking-tight ${
              tone === "bad"
                ? "text-destructive"
                : tone === "good"
                  ? "text-emerald-600 dark:text-emerald-400"
                  : ""
            }`}
          >
            <AnimatedNumber value={value} format={format} />
          </p>
          {delta !== null && (
            <span
              key={value}
              className="animate-in fade-in slide-in-from-bottom-1 text-xs font-medium text-primary duration-300"
            >
              +{delta.toLocaleString()}
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function StatusBadge({
  done,
  circuitBroken,
}: {
  done: boolean;
  circuitBroken: boolean;
}) {
  return <StatusDot status={testStatus(done, circuitBroken)} size="md" />;
}
