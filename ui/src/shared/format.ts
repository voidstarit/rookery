/**
 * formatCount formats a proto3 numeric field as a localized string. Proto3 JSON omits
 * default/zero values, so an absent counter is zero rather than missing data.
 */
export function formatCount(value: number | null | undefined): string {
  return (value ?? 0).toLocaleString();
}

/**
 * formatBytesIEC formats a byte count in binary units (B through PiB), showing one decimal only
 * for non-integral values and preserving the sign of negative counts.
 */
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

/**
 * formatRateIEC appends a per-second suffix to formatBytesIEC output for live traffic columns.
 */
export function formatRateIEC(value: number | null | undefined): string {
  return `${formatBytesIEC(value)}/s`;
}

/**
 * formatByteSize formats a byte count using binary steps with short labels (B, KB, MB, GB…).
 * The largest unit that keeps the value at or above 1 is used, so sizes below a
 * gigabyte stay in MB/KB instead of being shown as a fraction of a GB, and
 * 999 KB is never rendered as 0.9 MB.
 */
export function formatByteSize(value: number | null | undefined): string {
  const bytes = Number(value) || 0;
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const sign = bytes < 0 ? "-" : "";
  let scaled = Math.abs(bytes);
  let unit = 0;
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  let text = formatScaledBytes(scaled);
  // Rounding can push e.g. 1023.9 KB to "1024"; carry that into the next unit.
  if (Number(text) >= 1024 && unit < units.length - 1) {
    unit += 1;
    text = formatScaledBytes(scaled / 1024);
  }
  return `${sign}${text} ${units[unit]}`;
}

/**
 * formatScaledBytes renders an already-scaled byte value with 0, 1, or 2 decimals, trimming
 * trailing zeros and rendering zero as "0".
 */
function formatScaledBytes(scaled: number): string {
  if (scaled === 0) {
    return "0";
  }
  const digits = Number.isInteger(scaled) ? 0 : scaled < 10 ? 2 : 1;
  let text = scaled.toFixed(digits);
  if (text.includes(".")) {
    text = text.replace(/0+$/, "").replace(/\.$/, "");
  }
  return text;
}
