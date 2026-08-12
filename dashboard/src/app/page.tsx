"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { getToken, loginUrl } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

// The pitch, condensed from SCOPE.md's positioning section — the gap
// against k6 (single-machine free tier, distributed is paywalled),
// JMeter (manual master/slave node config), and Loader.io (10 concurrent
// clients on the free tier).
const WHY = [
  {
    title: "Actually distributed, actually free",
    body: "Real workers on real machines across providers and regions. Not a single-box free tier with distributed testing paywalled behind a subscription",
  },
  {
    title: "No scripting, no config sprawl",
    body: "Verify a target, pick a preset (or set your own VUs/duration/ramp), go. No test-scripting DSL to learn, no manually wiring up nodes",
  },
  {
    title: "Honest about its own limits",
    body: "The coordinator checks live fleet capacity before accepting a test. It refuses or clamps with a warning instead of silently under-delivering",
  },
  {
    title: "Shareable by default",
    body: "Every finished test gets a public report link and an embeddable badge. Drop it in a README, or wire the GitHub Action into your PR checks",
  },
];

export default function Home() {
  const router = useRouter();

  useEffect(() => {
    if (getToken()) router.replace("/tests");
  }, [router]);

  return (
    <main className="flex flex-1 flex-col items-center gap-16 px-6 py-16">
      <div className="flex flex-col items-center gap-6 text-center">
        <h1 className="text-4xl font-semibold tracking-tight">
          Sentry <span className="text-primary">Load</span>
        </h1>
        <p className="max-w-md text-muted-foreground">
          Load-test your own side project before it goes live. Verify a
          target, pick a preset, watch RPS and latency stream in live. Free,
          across real distributed workers
        </p>
        <Button size="lg" nativeButton={false} render={<a href={loginUrl()} />}>
          Log in with GitHub
        </Button>
      </div>

      <div className="grid w-full max-w-3xl gap-4 sm:grid-cols-2">
        {WHY.map((item) => (
          <Card key={item.title}>
            <CardContent>
              <h2 className="font-medium">{item.title}</h2>
              <p className="mt-1 text-sm text-muted-foreground">{item.body}</p>
            </CardContent>
          </Card>
        ))}
      </div>
    </main>
  );
}
