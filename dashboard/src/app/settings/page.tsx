"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ApiError,
  getToken,
  getWebhookSettings,
  setWebhookSettings,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

export default function SettingsPage() {
  const router = useRouter();
  const [webhookUrl, setWebhookUrlInput] = useState("");
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!getToken()) {
      router.replace("/");
      return;
    }
    getWebhookSettings()
      .then((s) => setWebhookUrlInput(s.webhook_url))
      .catch(() => {
        // Nothing configured yet reads the same as an empty string — no
        // error banner needed for the common first-visit case.
      })
      .finally(() => setLoaded(true));
  }, [router]);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSaved(false);
    setBusy(true);
    try {
      const result = await setWebhookSettings(webhookUrl.trim());
      setWebhookUrlInput(result.webhook_url);
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  function clear() {
    setWebhookUrlInput("");
  }

  if (!loaded) return null;

  return (
    <main className="mx-auto w-full max-w-xl flex-1 px-6 py-10">
      <h1 className="mb-2 text-2xl font-semibold tracking-tight">Settings</h1>
      <p className="mb-6 text-sm text-muted-foreground">
        Get notified in Discord or Slack the moment one of your tests
        finishes, including whether it circuit-broke
      </p>

      <Card>
        <CardHeader>
          <CardTitle>Chat webhook</CardTitle>
          <CardDescription>
            Paste an incoming webhook URL from Discord (Server Settings →
            Integrations → Webhooks) or Slack (Incoming Webhooks app). Leave
            it empty to turn notifications off.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={save} className="space-y-3">
            <div className="space-y-2">
              <Label htmlFor="webhook-url">Webhook URL</Label>
              <Input
                id="webhook-url"
                value={webhookUrl}
                onChange={(e) => {
                  setWebhookUrlInput(e.target.value);
                  setSaved(false);
                }}
                placeholder="https://discord.com/api/webhooks/…"
              />
            </div>

            {error && (
              <p className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {error}
              </p>
            )}
            {saved && !error && (
              <p className="rounded-lg bg-primary/10 px-3 py-2 text-sm text-primary">
                Saved.
              </p>
            )}

            <div className="flex gap-2">
              <Button type="submit" disabled={busy}>
                Save
              </Button>
              {webhookUrl && (
                <Button type="button" variant="outline" onClick={clear} disabled={busy}>
                  Clear
                </Button>
              )}
            </div>
          </form>
        </CardContent>
      </Card>
    </main>
  );
}
