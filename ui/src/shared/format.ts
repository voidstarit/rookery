/**
 * Formats a proto3 numeric field. Proto3 JSON omits default/zero values, so an
 * absent counter is zero rather than missing data.
 */
export function formatCount(value: number | null | undefined): string {
  return (value ?? 0).toLocaleString();
}

export function formatBytesIEC(value: number | null | undefined): string {
  const bytes = Number(value) || 0;
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let scaled = Math.abs(bytes);
  let unit = 0;
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  const sign = bytes < 0 ? "-" : "";
  const digits = unit === 0 || Number.isInteger(scaled) ? 0 : 1;
  return `${sign}${scaled.toFixed(digits)} ${units[unit]}`;
}

export function formatRateIEC(value: number | null | undefined): string {
  return `${formatBytesIEC(value)}/s`;
}
