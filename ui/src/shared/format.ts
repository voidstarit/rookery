/**
 * Formats a proto3 numeric field. Proto3 JSON omits default/zero values, so an
 * absent counter is zero rather than missing data.
 */
export function formatCount(value: number | null | undefined): string {
  return (value ?? 0).toLocaleString();
}
