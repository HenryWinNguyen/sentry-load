"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ApiError,
  CreateTestInput,
  TestSnapshot,
  createTest,
  getToken,
  listTests,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
import InfoTooltip from "@/components/InfoTooltip";

// Presets tuned to indie-launch scale (SCOPE.md M14), not raw enterprise
// configurability, just enough to get a first-timer to a sensible test
// without having to know what a VU or a ramp pattern is.
const PRESETS = [
  {
    key: "quick-check",
    label: "Quick Check",
    description: "60s, ~50 users. A fast sanity check before you ship",
    vus: "50",
    durationSeconds: "60",
    rampPattern: "steady" as const,
  },
  {
    key: "launch-day",
    label: "Launch Day",
    description: "5 min ramp to 200 users. Simulates a real traffic spike",
    vus: "200",
    durationSeconds: "300",
    rampPattern: "ramp" as const,
  },
  {
    key: "class-demo",
    label: "Class Demo",
    description: "2 min, steady moderate load. Good for showing off live",
    vus: "30",
    durationSeconds: "120",
    rampPattern: "steady" as const,
  },
];

const RAMP_PATTERNS = [
  {
    value: "steady" as const,
    label: "Steady",
    description: "Full load from second one. Good for a straightforward capacity check",
  },
  {
    value: "ramp" as const,
    label: "Ramp up",
    description: "Gradually builds from 0 to full load over the test. Shows you where things start to break",
  },
];

export default function TestsPage() {
  const router = useRouter();
  const [tests, setTests] = useState<TestSnapshot[] | null>(null);
  const [historyAvailable, setHistoryAvailable] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Deliberately empty, not pre-filled — a pre-filled example URL is easy
  // to submit by accident without noticing (it happened live: a test
  // silently ran against whichever worker's own localhost instead of the
  // real target). Leaving it blank forces a conscious choice; the
  // placeholder below still shows the expected shape.
  const [url, setUrl] = useState("");
  // Kept as strings, not numbers — a controlled number input bound to a
  // number forces the field back to "0" the instant it's cleared (empty
  // string coerces to 0), which means you can never actually delete the
  // value to type a new one. Parsed to a number only at submit time.
  const [vus, setVus] = useState("10");
  const [durationSeconds, setDurationSeconds] = useState("10");
  const [rampPattern, setRampPattern] =
    useState<(typeof RAMP_PATTERNS)[number]["value"]>("steady");
  const [workerCount, setWorkerCount] = useState("1");
  const [selectedPreset, setSelectedPreset] = useState<string | null>(null);

  function applyPreset(preset: (typeof PRESETS)[number]) {
    setVus(preset.vus);
    setDurationSeconds(preset.durationSeconds);
    setRampPattern(preset.rampPattern);
    setSelectedPreset(preset.key);
  }

  useEffect(() => {
    if (!getToken()) {
      router.replace("/");
      return;
    }
    listTests()
      .then(setTests)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 501) {
          setHistoryAvailable(false);
          setTests([]);
        } else {
          setError(err instanceof ApiError ? err.message : "Failed to load tests.");
        }
      });
  }, [router]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const input: CreateTestInput = {
        url,
        vus: Number(vus),
        duration_seconds: Number(durationSeconds),
        ramp_pattern: rampPattern,
        worker_count: Number(workerCount),
      };
      const { test_id, warning } = await createTest(input);
      router.push(
        warning
          ? `/tests/${test_id}?warning=${encodeURIComponent(warning)}`
          : `/tests/${test_id}`,
      );
    } catch (err) {
      if (err instanceof ApiError) {
        setError(
          err.retryAfterSeconds
            ? `${err.message} (try again in ${err.retryAfterSeconds}s)`
            : err.message,
        );
      } else {
        setError("Something went wrong.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto w-full max-w-2xl flex-1 px-6 py-10">
      <h1 className="mb-1 text-2xl font-semibold tracking-tight">New test</h1>
      <p className="mb-6 text-sm text-muted-foreground">
        Configure a load test against your app. Everything here is
        explained, no prior load-testing experience needed.
      </p>

      <Card>
        <CardContent>
          <form onSubmit={submit} className="space-y-6">
            <div className="space-y-1.5">
              <Label>Quick presets</Label>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                {PRESETS.map((preset) => (
                  <button
                    type="button"
                    key={preset.key}
                    onClick={() => applyPreset(preset)}
                    className={`cursor-pointer rounded-lg border p-3 text-left text-sm transition-colors ${
                      selectedPreset === preset.key
                        ? "border-primary bg-primary/5 ring-1 ring-primary"
                        : "border-border hover:border-foreground/30"
                    }`}
                  >
                    <p
                      className={
                        selectedPreset === preset.key
                          ? "font-medium text-primary"
                          : "font-medium"
                      }
                    >
                      {preset.label}
                    </p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {preset.description}
                    </p>
                  </button>
                ))}
              </div>
              <p className="text-xs text-muted-foreground">
                Or configure everything below manually.
              </p>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="url" className="flex items-center gap-1.5">
                Target URL
                <InfoTooltip>
                  The exact endpoint you want to load test. Your homepage,
                  an API route, whatever you&apos;re worried about
                </InfoTooltip>
              </Label>
              <Input
                id="url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                required
                placeholder="https://your-app.com/"
              />
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="vus" className="flex items-center gap-1.5">
                  Concurrent users
                  <InfoTooltip>
                    How many simulated visitors hit your app at once
                  </InfoTooltip>
                </Label>
                <Input
                  id="vus"
                  type="number"
                  min={1}
                  max={200}
                  value={vus}
                  onChange={(e) => {
                    setVus(e.target.value);
                    setSelectedPreset(null);
                  }}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="duration" className="flex items-center gap-1.5">
                  Duration
                  <InfoTooltip>How long the test runs</InfoTooltip>
                </Label>
                <div className="relative">
                  <Input
                    id="duration"
                    type="number"
                    min={1}
                    max={300}
                    value={durationSeconds}
                    onChange={(e) => {
                      setDurationSeconds(e.target.value);
                      setSelectedPreset(null);
                    }}
                  />
                  <span className="pointer-events-none absolute inset-y-0 right-2.5 flex items-center text-xs text-muted-foreground">
                    sec
                  </span>
                </div>
              </div>
            </div>

            <div className="space-y-1.5">
              <Label>Traffic pattern</Label>
              <div className="grid grid-cols-2 gap-2">
                {RAMP_PATTERNS.map((p) => (
                  <button
                    type="button"
                    key={p.value}
                    onClick={() => {
                      setRampPattern(p.value);
                      setSelectedPreset(null);
                    }}
                    className={`cursor-pointer rounded-lg border p-3 text-left text-sm transition-colors ${
                      rampPattern === p.value
                        ? "border-primary bg-primary/5 ring-1 ring-primary"
                        : "border-border hover:border-foreground/30"
                    }`}
                  >
                    <p className={rampPattern === p.value ? "font-medium text-primary" : "font-medium"}>
                      {p.label}
                    </p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {p.description}
                    </p>
                  </button>
                ))}
              </div>
            </div>

            <details className="rounded-lg border p-3 text-sm">
              <summary className="cursor-pointer font-medium">
                Advanced: multiple worker machines
              </summary>
              <div className="mt-3 space-y-1.5">
                <Label htmlFor="workers" className="flex items-center gap-1.5">
                  Worker machines
                  <InfoTooltip>
                    Splits the load across multiple worker machines running
                    in parallel, for genuinely distributed load instead of
                    one machine&apos;s worth. Leave at 1 unless you know you
                    have more than one worker running
                  </InfoTooltip>
                </Label>
                <Input
                  id="workers"
                  type="number"
                  min={1}
                  max={5}
                  value={workerCount}
                  onChange={(e) => setWorkerCount(e.target.value)}
                />
              </div>
            </details>

            {error && (
              <p className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {error}
              </p>
            )}

            <div>
              <Button type="submit" disabled={busy} className="w-full" size="lg">
                {busy ? "Submitting…" : "Run test"}
              </Button>
              <p className="mt-2 text-xs text-muted-foreground">
                Targeting a domain that isn&apos;t already verified?{" "}
                <Link href="/domains" className="text-primary hover:underline">
                  Verify it first
                </Link>
                .
              </p>
            </div>
          </form>
        </CardContent>
      </Card>

      <h2 className="mb-3 mt-10 text-lg font-semibold tracking-tight">
        History
      </h2>
      {!historyAvailable && (
        <p className="text-sm text-muted-foreground">
          Test history isn&apos;t available on this server (Postgres not
          configured). You can still run tests, just not browse past ones
        </p>
      )}
      {historyAvailable && tests?.length === 0 && (
        <p className="text-sm text-muted-foreground">No finished tests yet.</p>
      )}
      {tests && tests.length > 0 && (
        <Card>
          <CardContent className="divide-y p-0">
            {tests.map((t) => {
              const errorRate =
                t.total_requests > 0
                  ? ((t.total_errors / t.total_requests) * 100).toFixed(1)
                  : "0.0";
              return (
                <Link
                  key={t.test_id}
                  href={`/tests/${t.test_id}`}
                  className="flex items-center justify-between px-4 py-3 first:rounded-t-xl last:rounded-b-xl hover:bg-muted"
                >
                  <div>
                    <p className="break-all text-sm font-medium">{t.url}</p>
                    <p className="font-mono text-xs text-muted-foreground">
                      {t.test_id.slice(0, 8)}
                    </p>
                  </div>
                  <div className="text-right text-sm">
                    <p>
                      {t.total_requests} req · {errorRate}% errors
                    </p>
                    {t.circuit_broken && (
                      <p className="text-amber-600 dark:text-amber-400">
                        circuit-broken
                      </p>
                    )}
                  </div>
                </Link>
              );
            })}
          </CardContent>
        </Card>
      )}
    </main>
  );
}
