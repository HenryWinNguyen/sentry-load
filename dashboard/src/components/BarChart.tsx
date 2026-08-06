// Small hand-rolled SVG bar chart — pairs with LineChart, same reasoning:
// too few points to justify a charting dependency.
export default function BarChart({
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
  const gap = 2;

  if (points.length === 0) {
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
  const plotTop = padding;
  const plotBottom = height - padding;
  const barWidth = (width - padding * 2) / points.length;
  const last = points[points.length - 1];
  const realMin = Math.min(...points);
  const realMax = Math.max(...points);

  // Same reasoning as LineChart: axis labels are a plain HTML column next
  // to the SVG, not text inside it, so they're never subject to the
  // non-uniform stretch preserveAspectRatio="none" applies once the
  // container is much wider than the chart's internal coordinate space.
  const gridValues = [max, max / 2, 0];

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
            const y = plotBottom - (v / max) * (plotBottom - plotTop);
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
          {points.map((v, i) => {
            const barHeight = Math.max((v / max) * (plotBottom - plotTop), 1);
            const x = i * barWidth;
            const y = plotBottom - barHeight;
            const isLatest = i === points.length - 1;
            return (
              <rect
                key={i}
                x={x}
                y={y}
                width={Math.max(barWidth - gap, 1)}
                height={barHeight}
                fill={color}
                fillOpacity={isLatest ? 1 : 0.45}
                rx={1}
                style={{
                  transformBox: "fill-box",
                  transformOrigin: "bottom",
                  animation: "grow-in 300ms ease-out",
                }}
              />
            );
          })}
        </svg>
      </div>
      <div className="mt-1 flex justify-between text-xs text-muted-foreground">
        <span>
          range: {formatValue(realMin)}
          {realMin !== realMax ? `–${formatValue(realMax)}` : " (steady)"}
        </span>
        <span className="flex items-center gap-1 font-medium text-foreground">
          <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: color }} />
          latest: {formatValue(last)}
        </span>
      </div>
    </div>
  );
}
