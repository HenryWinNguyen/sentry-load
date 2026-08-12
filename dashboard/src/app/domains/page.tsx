"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ApiError,
  DomainChallenge,
  getToken,
  requestDomainChallenge,
  verifyDomain,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default function DomainsPage() {
  const router = useRouter();
  const [domain, setDomain] = useState("");
  const [challenge, setChallenge] = useState<DomainChallenge | null>(null);
  const [verified, setVerified] = useState<boolean | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!getToken()) router.replace("/");
  }, [router]);

  async function requestChallenge(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setVerified(null);
    setBusy(true);
    try {
      setChallenge(await requestDomainChallenge(domain));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  async function check(method: "dns" | "well-known") {
    if (!challenge) return;
    setError(null);
    setBusy(true);
    try {
      const result = await verifyDomain(challenge.domain, method);
      setVerified(result.verified);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto w-full max-w-xl flex-1 px-6 py-10">
      <h1 className="mb-2 text-2xl font-semibold tracking-tight">
        Verify a domain
      </h1>
      <p className="mb-6 text-sm text-muted-foreground">
        Proving you own a domain is what separates a load tester from a
        DDoS-as-a-service tool. Every target needs it, unless it&apos;s
        already on the built-in allowlist (e.g. localhost)
      </p>

      <form onSubmit={requestChallenge} className="mb-8 flex gap-2">
        <Input
          value={domain}
          onChange={(e) => setDomain(e.target.value)}
          placeholder="example.com"
          required
        />
        <Button type="submit" disabled={busy}>
          Get challenge
        </Button>
      </form>

      {error && (
        <p className="mb-4 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}

      {challenge && (
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>DNS TXT record</CardTitle>
              <CardDescription>
                Add a TXT record at <code>{challenge.dns_record}</code> with
                this value:
              </CardDescription>
            </CardHeader>
            <CardContent>
              <code className="block break-all rounded-lg bg-muted p-2 text-xs">
                {challenge.dns_value}
              </code>
              <Button
                variant="outline"
                size="sm"
                onClick={() => check("dns")}
                disabled={busy}
                className="mt-3"
              >
                Check DNS record
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Well-known file</CardTitle>
              <CardDescription>Serve exactly this token at:</CardDescription>
            </CardHeader>
            <CardContent>
              <code className="block break-all rounded-lg bg-muted p-2 text-xs">
                {challenge.well_known_url}
              </code>
              <Button
                variant="outline"
                size="sm"
                onClick={() => check("well-known")}
                disabled={busy}
                className="mt-3"
              >
                Check well-known file
              </Button>
            </CardContent>
          </Card>

          {verified !== null && (
            <p
              className={`rounded-lg px-3 py-2 text-sm ${
                verified
                  ? "bg-primary/10 text-primary"
                  : "bg-amber-500/10 text-amber-600 dark:text-amber-400"
              }`}
            >
              {verified
                ? "Verified. You can target this domain now"
                : "Not verified yet. Set up the record/file and try again"}
            </p>
          )}
        </div>
      )}
    </main>
  );
}
