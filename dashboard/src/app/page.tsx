"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { getToken, loginUrl } from "@/lib/api";
import { Button } from "@/components/ui/button";

export default function Home() {
  const router = useRouter();

  useEffect(() => {
    if (getToken()) router.replace("/tests");
  }, [router]);

  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-6 px-6 text-center">
      <h1 className="text-4xl font-semibold tracking-tight">
        Sentry <span className="text-primary">Load</span>
      </h1>
      <p className="max-w-md text-muted-foreground">
        Load-test your own side project before it goes live. Verify a domain,
        pick a config, watch RPS and latency stream in live.
      </p>
      <Button size="lg" nativeButton={false} render={<a href={loginUrl()} />}>
        Log in with GitHub
      </Button>
    </main>
  );
}
