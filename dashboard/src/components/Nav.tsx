"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { clearToken, getToken } from "@/lib/api";
import { Button } from "@/components/ui/button";

export default function Nav() {
  const router = useRouter();
  const [loggedIn, setLoggedIn] = useState(false);

  useEffect(() => {
    // localStorage doesn't exist during server rendering, so this can only
    // be read after mount — the deferred setState is the point here, not
    // an oversight the lint rule's general case doesn't account for.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoggedIn(!!getToken());
  }, []);

  function logout() {
    clearToken();
    router.push("/");
  }

  return (
    <nav className="flex items-center justify-between border-b px-6 py-3">
      <Link href={loggedIn ? "/tests" : "/"} className="font-semibold tracking-tight">
        Sentry <span className="text-primary">Load</span>
      </Link>
      {loggedIn && (
        <div className="flex items-center gap-1 text-sm">
          <Button
            variant="ghost"
            size="sm"
            nativeButton={false}
            render={<Link href="/tests" />}
          >
            Tests
          </Button>
          <Button
            variant="ghost"
            size="sm"
            nativeButton={false}
            render={<Link href="/domains" />}
          >
            Domains
          </Button>
          <Button variant="ghost" size="sm" onClick={logout}>
            Log out
          </Button>
        </div>
      )}
    </nav>
  );
}
