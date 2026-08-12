"use client";

import { use, useEffect, useState } from "react";
import Link from "next/link";
import { ApiError, TestSnapshot, getPublicReport } from "@/lib/api";
import { Button } from "@/components/ui/button";
import StatusDot, { testStatus } from "@/components/StatusDot";
import InfoTooltip from "@/components/InfoTooltip";
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

// Smaller, letter-spaced, muted column labels read as a data table built
// with intent — the shadcn default (same size/weight as the body text)
// doesn't visually separate "this is a header" from "this is a value."
const TH_LABEL = "h-8 text-[11px] font-medium tracking-wide text-muted-foreground uppercase";

// A shared report is meant to be viewable by anyone holding the link, no
// account needed — this page never calls anything that requires a bearer
// token (see lib/api.ts's getPublicReport, which deliberately never
// attaches one).
export default function PublicReportPage({
  params,
}: {
  params: Promise<{ token: string }>;
}) {
  const { token } = use(params);
  const [snap, setSnap] = useState<TestSnapshot | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getPublicReport(token)
      .then(setSnap)
      .catch((err) => {
        setError(
          err instanceof ApiError && err.status === 404
            ? "This report link doesn't exist, or hasn't been shared."
            : "Failed to load this report.",
        );
      });
  }, [token]);

  if (error) {
    return (
      <main className="mx-auto w-full max-w-2xl flex-1 px-6 py-10 text-center">
        <p className="text-muted-foreground">{error}</p>
      </main>
    );
  }

  if (!snap) {
    return (
      <main className="mx-auto w-full max-w-2xl flex-1 px-6 py-10">
        <p className="text-muted-foreground">Loading…</p>
      </main>
    );
  }

  const errorRate =
    snap.total_requests > 0
      ? (snap.total_errors / snap.total_requests) * 100
      : 0;

  return (
    <main className="mx-auto w-full max-w-2xl flex-1 px-6 py-10">
      <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Load test report
          </p>
          <h1 className="break-all text-2xl font-semibold tracking-tight">
            {snap.label || snap.url}
          </h1>
          {snap.label && (
            <p className="break-all text-sm text-muted-foreground">{snap.url}</p>
          )}
        </div>
        <StatusDot status={testStatus(true, snap.circuit_broken)} size="md" />
      </div>

      <div className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Total requests" value={snap.total_requests} />
        <Stat label="Total errors" value={snap.total_errors} />
        <Stat
          label="Error rate"
          value={`${errorRate.toFixed(1)}%`}
          tone={errorRate > 5 ? "bad" : "good"}
        />
        <Stat label="Combined RPS" value={snap.combined_rps.toFixed(1)} />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5">
            Per worker
            <InfoTooltip>
              Each worker&apos;s own latency percentiles, shown separately
              rather than averaged together.
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
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <div className="mt-8 flex items-center justify-between rounded-xl border bg-muted/50 p-4">
        <p className="text-sm text-muted-foreground">
          Load tested by <span className="font-medium text-primary">Sentry Load</span>.
        </p>
        <Button nativeButton={false} render={<Link href="/" />}>
          Test your own project
        </Button>
      </div>
    </main>
  );
}

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | string;
  tone?: "good" | "bad";
}) {
  return (
    <Card>
      <CardContent>
        <p className="text-xs text-muted-foreground">{label}</p>
        <p
          className={`text-2xl font-semibold tracking-tight ${
            tone === "bad"
              ? "text-destructive"
              : tone === "good"
                ? "text-emerald-600 dark:text-emerald-400"
                : ""
          }`}
        >
          {value}
        </p>
      </CardContent>
    </Card>
  );
}
