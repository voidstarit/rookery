/**
 * formatDate renders an ISO timestamp as a locale medium date and short time, returning an em dash
 * when the value is missing or unparseable.
 */
export function formatDate(value: string | undefined): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  return date.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}
