export type MonitorSample = {
  cpu: number | null;
  rss: number;
  read: number | null;
  write: number | null;
  netRead: number | null;
  netWrite: number | null;
};

export type ChartSeries = {
  values: Array<number | null>;
  color: string;
};

export function formatBytes(bytes: number): string {
  const value = Number(bytes) || 0;
  if (value >= 1024 ** 3) {
    return `${(value / 1024 ** 3).toFixed(value % 1024 ** 3 === 0 ? 0 : 1)} GB`;
  }
  if (value >= 1024 ** 2) {
    return `${(value / 1024 ** 2).toFixed(value % 1024 ** 2 === 0 ? 0 : 1)} MB`;
  }
  if (value >= 1024) {
    return `${(value / 1024).toFixed(value % 1024 === 0 ? 0 : 1)} KB`;
  }
  return `${value} B`;
}

export function formatMemory(mb: number): string {
  if (mb >= 1024) {
    const gb = mb / 1024;
    return `${Number.isInteger(gb) ? gb : gb.toFixed(1)} GB`;
  }
  return `${mb} MB`;
}

export function formatDuration(seconds: number): string {
  const value = Math.max(0, Math.round(Number(seconds) || 0));
  const days = Math.floor(value / 86400);
  const hours = Math.floor((value % 86400) / 3600);
  const minutes = Math.floor((value % 3600) / 60);
  const secs = value % 60;
  if (days > 0) {
    return `${days}d ${hours}h`;
  }
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${secs}s`;
  }
  return `${secs}s`;
}

function chartCanvas(canvas: HTMLCanvasElement | null) {
  if (!canvas || typeof canvas.getContext !== "function") {
    return null;
  }
  const context = canvas.getContext("2d");
  if (!context) {
    return null;
  }
  const dpr = window.devicePixelRatio || 1;
  const width = Math.max(280, Math.round(canvas.getBoundingClientRect().width || 620));
  const height = 150;
  const pixelWidth = Math.round(width * dpr);
  const pixelHeight = Math.round(height * dpr);
  if (canvas.width !== pixelWidth || canvas.height !== pixelHeight) {
    canvas.width = pixelWidth;
    canvas.height = pixelHeight;
  }
  context.setTransform(dpr, 0, 0, dpr, 0, 0);
  return { context, width, height };
}

export function drawMetricChart(
  canvas: HTMLCanvasElement | null,
  series: ChartSeries[],
  options: {
    max?: number;
    marker?: number;
    format?: (value: number) => string;
  } = {}
): void {
  const prepared = chartCanvas(canvas);
  if (!prepared) {
    return;
  }
  const { context, width, height } = prepared;
  context.clearRect(0, 0, width, height);

  const padding = { left: 52, right: 10, top: 12, bottom: 18 };
  const plotWidth = Math.max(1, width - padding.left - padding.right);
  const plotHeight = Math.max(1, height - padding.top - padding.bottom);
  const format = options.format || ((value: number) => String(Math.round(value)));
  const allValues = series
    .flatMap((item) => item.values || [])
    .filter((value): value is number => Number.isFinite(value));
  let maxValue = options.max || 0;
  if (!maxValue) {
    maxValue = allValues.length ? Math.max(...allValues) * 1.15 : 0;
  }
  if (maxValue <= 0) {
    maxValue = 1;
  }

  context.strokeStyle = "rgba(148, 163, 184, 0.14)";
  context.fillStyle = "#94a3b8";
  context.font = "10px SFMono-Regular, Consolas, monospace";
  context.lineWidth = 1;
  for (let line = 0; line <= 2; line += 1) {
    const y = padding.top + (plotHeight * line) / 2;
    context.beginPath();
    context.moveTo(padding.left, y);
    context.lineTo(padding.left + plotWidth, y);
    context.stroke();
    const label = format(maxValue * (1 - line / 2));
    context.fillText(label, 4, y + 3);
  }

  const marker = Number(options.marker);
  if (Number.isFinite(marker) && marker > 0 && marker <= maxValue) {
    const y = padding.top + plotHeight - (marker / maxValue) * plotHeight;
    context.save();
    context.setLineDash([4, 4]);
    context.strokeStyle = "rgba(226, 232, 240, 0.35)";
    context.beginPath();
    context.moveTo(padding.left, y);
    context.lineTo(padding.left + plotWidth, y);
    context.stroke();
    context.restore();
  }

  let drewPoint = false;
  for (const item of series) {
    const values = item.values || [];
    if (values.length === 0) {
      continue;
    }
    context.strokeStyle = item.color;
    context.fillStyle = item.color;
    context.lineWidth = 2;
    context.lineJoin = "round";
    context.lineCap = "round";
    context.beginPath();
    let open = false;
    for (let index = 0; index < values.length; index += 1) {
      const value = values[index];
      if (!Number.isFinite(value)) {
        open = false;
        continue;
      }
      const x =
        values.length === 1
          ? padding.left + plotWidth
          : padding.left + (plotWidth * index) / (values.length - 1);
      const y =
        padding.top +
        plotHeight -
        Math.min(1, Math.max(0, (value as number) / maxValue)) * plotHeight;
      if (!open) {
        context.moveTo(x, y);
        open = true;
      } else {
        context.lineTo(x, y);
      }
      drewPoint = true;
    }
    context.stroke();
    if (values.length === 1 && Number.isFinite(values[0])) {
      const x = padding.left + plotWidth;
      const y =
        padding.top +
        plotHeight -
        Math.min(1, Math.max(0, (values[0] as number) / maxValue)) * plotHeight;
      context.beginPath();
      context.arc(x, y, 2.5, 0, Math.PI * 2);
      context.fill();
    }
  }
  if (!drewPoint) {
    context.fillStyle = "#64748b";
    context.font = "11px sans-serif";
    context.fillText("No samples yet", padding.left, padding.top + plotHeight / 2);
  }
}
