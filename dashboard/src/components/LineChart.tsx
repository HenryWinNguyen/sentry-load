// Small hand-rolled SVG line chart — the data (a handful of points ticking
// in once a second) is way too simple to justify a charting dependency.
export default function LineChart({
  points,
  color = "var(--primary)",
  height = 180,
  formatValue = (v: number) => v.toFixed(0),
}: {
  points: number[];
  color?: string;
  height?: number;
  formatValue?: (v: number) => string;
}) {
  const width = 400;
  const padding = 8;

  if (points.length < 2) {
    return (
      <div
        style={{ height }}
        className="flex items-center justify-center text-xs text-muted-foreground"
      >
        Waiting for data…
      </div>
    );
  }

  const max = Math.max(...points, 1);
  const min = Math.min(...points, 0);
  const range = max - min || 1;
  const plotTop = padding;
  const plotBottom = height - padding;

  const toXY = (value: number, index: number): [number, number] => {
    const x = padding + (index / (points.length - 1)) * (width - padding * 2);
    const y = plotBottom - ((value - min) / range) * (plotBottom - plotTop);
    return [x, y];
  };

  const path = points
    .map((v, i) => toXY(v, i))
    .map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`)
    .join(" ");

  const last = points[points.length - 1];
  const realMin = Math.min(...points);
  const realMax = Math.max(...points);

  // Three gridlines — max, midpoint, min of the plotted scale — is enough
  // to give a sense of magnitude without turning this into a real charting
  // library's worth of tick logic. The labels live in a plain HTML column
  // next to the SVG, not inside it — text placed inside an SVG scaled with
  // preserveAspectRatio="none" gets stretched non-uniformly the moment the
  // container's aspect ratio doesn't match the viewBox, which is exactly
  // what a full-width chart card does.
  const gridValues = [max, min + range / 2, min];

  return (
    <div>
      <div className="flex gap-2">
        <div
          className="flex shrink-0 flex-col justify-between py-2 text-right text-[10px] text-muted-foreground"
          style={{ height }}
        >
          {gridValues.map((v, i) => (
            <span key={i}>{formatValue(v)}</span>
          ))}
        </div>
        <svg
          viewBox={`0 0 ${width} ${height}`}
          className="w-full flex-1"
          style={{ height }}
          preserveAspectRatio="none"
        >
          {gridValues.map((v, i) => {
            const y = plotBottom - ((v - min) / range) * (plotBottom - plotTop);
            return (
              <line
                key={i}
                x1={0}
                y1={y}
                x2={width}
                y2={y}
                stroke="var(--border)"
                strokeWidth={1}
              />
            );
          })}
          <path d={path} fill="none" stroke={color} strokeWidth={2} />
        </svg>
      </div>
      <div className="mt-1 flex justify-between text-xs text-muted-foreground">
        <span>
          range: {formatValue(realMin)}
          {realMin !== realMax ? `–${formatValue(realMax)}` : " (steady)"}
        </span>
        <span>latest: {formatValue(last)}</span>
      </div>
    </div>
  );
}
