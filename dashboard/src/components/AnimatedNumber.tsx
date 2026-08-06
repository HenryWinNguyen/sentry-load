"use client";

import { useEffect, useRef, useState } from "react";

// Counts smoothly from the previous value to the new one instead of
// snapping instantly — live ticks arriving once a second read as
// "processing" rather than "jumping," without needing a charting/
// animation library for one number.
export default function AnimatedNumber({
  value,
  format = (v: number) => Math.round(v).toLocaleString(),
  durationMs = 500,
}: {
  value: number;
  format?: (v: number) => string;
  durationMs?: number;
}) {
  const [display, setDisplay] = useState(value);
  const fromRef = useRef(value);

  useEffect(() => {
    const from = fromRef.current;
    const to = value;
    if (from === to) return;

    let raf: number;
    let start: number | null = null;
    const step = (timestamp: number) => {
      if (start === null) start = timestamp;
      const t = Math.min((timestamp - start) / durationMs, 1);
      const eased = 1 - Math.pow(1 - t, 3); // ease-out cubic
      setDisplay(from + (to - from) * eased);
      if (t < 1) {
        raf = requestAnimationFrame(step);
      } else {
        fromRef.current = to;
      }
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [value, durationMs]);

  return <>{format(display)}</>;
}
