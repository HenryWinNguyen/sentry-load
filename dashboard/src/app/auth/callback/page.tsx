"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { setToken } from "@/lib/api";

function CallbackInner() {
  const router = useRouter();
  const searchParams = useSearchParams();

  useEffect(() => {
    const token = searchParams.get("token");
    if (!token) {
      router.replace("/");
      return;
    }
    setToken(token);
    router.replace("/tests");
  }, [router, searchParams]);

  return (
    <main className="flex flex-1 items-center justify-center">
      <p className="text-zinc-500">Signing you in…</p>
    </main>
  );
}

// useSearchParams requires a Suspense boundary in the App Router — the
// coordinator's redirect (see coordinator/handlers.go's
// handleGithubCallback) lands here with ?token=&github_login=.
export default function AuthCallbackPage() {
  return (
    <Suspense
      fallback={
        <main className="flex flex-1 items-center justify-center">
          <p className="text-zinc-500">Signing you in…</p>
        </main>
      }
    >
      <CallbackInner />
    </Suspense>
  );
}
