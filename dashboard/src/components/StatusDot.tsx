// A single small dot + label is used for every test-status indicator in
// the app — the test detail page's header, its per-worker table rows,
// and the public report page — rather than each spot inventing its own
// tinted-pill badge. One consistent visual grammar for "what state is
// this in" reads as considered; a different badge style per instance
// reads as assembled from whatever the component library shipped.
const STATUS_STYLES = {
  running: { dot: "bg-primary", text: "text-foreground", pulse: true },
  done: { dot: "bg-emerald-500", text: "text-foreground", pulse: false },
  "circuit-broken": {
    dot: "bg-amber-500",
    text: "text-amber-700 dark:text-amber-400",
    pulse: false,
  },
} as const;

export type TestStatus = keyof typeof STATUS_STYLES;

export function testStatus(done: boolean, circuitBroken: boolean): TestStatus {
  if (circuitBroken) return "circuit-broken";
  if (done) return "done";
  return "running";
}

export function statusLabel(status: TestStatus): string {
  switch (status) {
    case "circuit-broken":
      return "Circuit-broken";
    case "done":
      return "Done";
    case "running":
      return "Running";
  }
}

export default function StatusDot({
  status,
  label,
  size = "sm",
}: {
  status: TestStatus;
  label?: string;
  size?: "sm" | "md";
}) {
  const style = STATUS_STYLES[status];
  const dotSize = size === "md" ? "h-2 w-2" : "h-1.5 w-1.5";
  return (
    <span className={`inline-flex items-center gap-2 font-medium ${style.text}`}>
      <span className={`relative flex ${dotSize}`}>
        {style.pulse && (
          <span
            className={`absolute inline-flex h-full w-full animate-ping rounded-full opacity-60 ${style.dot}`}
          />
        )}
        <span className={`relative inline-flex ${dotSize} rounded-full ${style.dot}`} />
      </span>
      {label ?? statusLabel(status)}
    </span>
  );
}
