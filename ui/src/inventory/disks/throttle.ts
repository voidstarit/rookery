import type { DiskThrottleLimits } from "../types";
import { formatBytesIEC, formatCount, formatRateIEC } from "../../shared/format";

export type ThrottleMode = "none" | "total" | "directional";

export type ThrottleDraft = {
  bpsMode: ThrottleMode;
  bpsTotal: string;
  bpsTotalMax: string;
  bpsTotalMaxLength: string;
  bpsRead: string;
  bpsReadMax: string;
  bpsReadMaxLength: string;
  bpsWrite: string;
  bpsWriteMax: string;
  bpsWriteMaxLength: string;
  iopsMode: ThrottleMode;
  iopsTotal: string;
  iopsTotalMax: string;
  iopsTotalMaxLength: string;
  iopsRead: string;
  iopsReadMax: string;
  iopsReadMaxLength: string;
  iopsWrite: string;
  iopsWriteMax: string;
  iopsWriteMaxLength: string;
  iopsSize: string;
};

export function emptyThrottleDraft(): ThrottleDraft {
  return {
    bpsMode: "none",
    bpsTotal: "",
    bpsTotalMax: "",
    bpsTotalMaxLength: "",
    bpsRead: "",
    bpsReadMax: "",
    bpsReadMaxLength: "",
    bpsWrite: "",
    bpsWriteMax: "",
    bpsWriteMaxLength: "",
    iopsMode: "none",
    iopsTotal: "",
    iopsTotalMax: "",
    iopsTotalMaxLength: "",
    iopsRead: "",
    iopsReadMax: "",
    iopsReadMaxLength: "",
    iopsWrite: "",
    iopsWriteMax: "",
    iopsWriteMaxLength: "",
    iopsSize: "",
  };
}

export function inferMode(limits: DiskThrottleLimits | undefined, family: "bps" | "iops"): ThrottleMode {
  if (!limits) {
    return "none";
  }
  if (family === "bps") {
    if ((limits.bps_total ?? 0) > 0 || (limits.bps_total_max ?? 0) > 0) {
      return "total";
    }
    if ((limits.bps_read ?? 0) > 0 || (limits.bps_write ?? 0) > 0) {
      return "directional";
    }
    return "none";
  }
  if ((limits.iops_total ?? 0) > 0 || (limits.iops_total_max ?? 0) > 0) {
    return "total";
  }
  if ((limits.iops_read ?? 0) > 0 || (limits.iops_write ?? 0) > 0) {
    return "directional";
  }
  return "none";
}

export function draftFromLimits(limits: DiskThrottleLimits | undefined): ThrottleDraft {
  const draft = emptyThrottleDraft();
  if (!limits) {
    return draft;
  }
  const value = (input: number | undefined) => (input && input > 0 ? String(input) : "");
  return {
    ...draft,
    bpsMode: inferMode(limits, "bps"),
    bpsTotal: value(limits.bps_total),
    bpsTotalMax: value(limits.bps_total_max),
    bpsTotalMaxLength: value(limits.bps_total_max_length),
    bpsRead: value(limits.bps_read),
    bpsReadMax: value(limits.bps_read_max),
    bpsReadMaxLength: value(limits.bps_read_max_length),
    bpsWrite: value(limits.bps_write),
    bpsWriteMax: value(limits.bps_write_max),
    bpsWriteMaxLength: value(limits.bps_write_max_length),
    iopsMode: inferMode(limits, "iops"),
    iopsTotal: value(limits.iops_total),
    iopsTotalMax: value(limits.iops_total_max),
    iopsTotalMaxLength: value(limits.iops_total_max_length),
    iopsRead: value(limits.iops_read),
    iopsReadMax: value(limits.iops_read_max),
    iopsReadMaxLength: value(limits.iops_read_max_length),
    iopsWrite: value(limits.iops_write),
    iopsWriteMax: value(limits.iops_write_max),
    iopsWriteMaxLength: value(limits.iops_write_max_length),
    iopsSize: value(limits.iops_size),
  };
}

export function validateDraft(draft: ThrottleDraft): string | null {
  const bpsError = validateFamily(draft, "bps");
  if (bpsError) {
    return bpsError;
  }
  const iopsError = validateFamily(draft, "iops");
  if (iopsError) {
    return iopsError;
  }
  const bpsBase = familyBase(draft, "bps");
  const iopsBase = familyBase(draft, "iops");
  if (!bpsBase && !iopsBase) {
    return "Set at least one BPS or IOPS limit.";
  }
  if (number(draft.iopsSize) > 0 && !iopsBase) {
    return "IOPS size requires an IOPS limit.";
  }
  return null;
}

export function limitsFromDraft(draft: ThrottleDraft): DiskThrottleLimits {
  const limits: DiskThrottleLimits = {};
  if (draft.bpsMode === "total") {
    setValue(limits, "bps_total", draft.bpsTotal);
    setValue(limits, "bps_total_max", draft.bpsTotalMax);
    setValue(limits, "bps_total_max_length", draft.bpsTotalMaxLength);
  } else if (draft.bpsMode === "directional") {
    setValue(limits, "bps_read", draft.bpsRead);
    setValue(limits, "bps_read_max", draft.bpsReadMax);
    setValue(limits, "bps_read_max_length", draft.bpsReadMaxLength);
    setValue(limits, "bps_write", draft.bpsWrite);
    setValue(limits, "bps_write_max", draft.bpsWriteMax);
    setValue(limits, "bps_write_max_length", draft.bpsWriteMaxLength);
  }
  if (draft.iopsMode === "total") {
    setValue(limits, "iops_total", draft.iopsTotal);
    setValue(limits, "iops_total_max", draft.iopsTotalMax);
    setValue(limits, "iops_total_max_length", draft.iopsTotalMaxLength);
  } else if (draft.iopsMode === "directional") {
    setValue(limits, "iops_read", draft.iopsRead);
    setValue(limits, "iops_read_max", draft.iopsReadMax);
    setValue(limits, "iops_read_max_length", draft.iopsReadMaxLength);
    setValue(limits, "iops_write", draft.iopsWrite);
    setValue(limits, "iops_write_max", draft.iopsWriteMax);
    setValue(limits, "iops_write_max_length", draft.iopsWriteMaxLength);
  }
  setValue(limits, "iops_size", draft.iopsSize);
  return limits;
}

export function formatThrottleSummary(limits: DiskThrottleLimits | undefined): string {
  if (!limits) {
    return "";
  }
  const parts: string[] = [];
  if ((limits.bps_total ?? 0) > 0) {
    parts.push(`${formatRateIEC(limits.bps_total)} total`);
  } else {
    if ((limits.bps_read ?? 0) > 0) {
      parts.push(`${formatRateIEC(limits.bps_read)} read`);
    }
    if ((limits.bps_write ?? 0) > 0) {
      parts.push(`${formatRateIEC(limits.bps_write)} write`);
    }
  }
  if ((limits.iops_total ?? 0) > 0) {
    parts.push(`${formatCount(limits.iops_total)} IOPS total`);
  } else {
    if ((limits.iops_read ?? 0) > 0) {
      parts.push(`${formatCount(limits.iops_read)} read IOPS`);
    }
    if ((limits.iops_write ?? 0) > 0) {
      parts.push(`${formatCount(limits.iops_write)} write IOPS`);
    }
  }
  if ((limits.iops_size ?? 0) > 0) {
    parts.push(`${formatBytesIEC(limits.iops_size)} I/O`);
  }
  return parts.join(" · ");
}

type FamilyValues = {
  mode: ThrottleMode;
  total: string;
  totalMax: string;
  totalMaxLength: string;
  read: string;
  readMax: string;
  readMaxLength: string;
  write: string;
  writeMax: string;
  writeMaxLength: string;
};

function familyValues(draft: ThrottleDraft, family: "bps" | "iops"): FamilyValues {
  if (family === "bps") {
    return {
      mode: draft.bpsMode,
      total: draft.bpsTotal,
      totalMax: draft.bpsTotalMax,
      totalMaxLength: draft.bpsTotalMaxLength,
      read: draft.bpsRead,
      readMax: draft.bpsReadMax,
      readMaxLength: draft.bpsReadMaxLength,
      write: draft.bpsWrite,
      writeMax: draft.bpsWriteMax,
      writeMaxLength: draft.bpsWriteMaxLength,
    };
  }
  return {
    mode: draft.iopsMode,
    total: draft.iopsTotal,
    totalMax: draft.iopsTotalMax,
    totalMaxLength: draft.iopsTotalMaxLength,
    read: draft.iopsRead,
    readMax: draft.iopsReadMax,
    readMaxLength: draft.iopsReadMaxLength,
    write: draft.iopsWrite,
    writeMax: draft.iopsWriteMax,
    writeMaxLength: draft.iopsWriteMaxLength,
  };
}

function validateFamily(draft: ThrottleDraft, family: "bps" | "iops"): string | null {
  const values = familyValues(draft, family);
  const label = family === "bps" ? "BPS" : "IOPS";
  if (values.mode === "none") {
    return null;
  }
  const negative = baseAndBursts(values).find(([, raw]) => number(raw) < 0);
  if (negative) {
    return `${label} ${negative[0]} must not be negative.`;
  }
  if (values.mode === "total") {
    const base = number(values.total);
    if (base <= 0) {
      return `${label} total limit must be greater than zero.`;
    }
    return validateBurst(`${label} total`, base, number(values.totalMax), number(values.totalMaxLength));
  }
  if (number(values.read) <= 0 && number(values.write) <= 0) {
    return `${label} read or write limit must be greater than zero.`;
  }
  return (
    validateBurst(`${label} read`, number(values.read), number(values.readMax), number(values.readMaxLength)) ||
    validateBurst(`${label} write`, number(values.write), number(values.writeMax), number(values.writeMaxLength))
  );
}

function validateBurst(label: string, base: number, max: number, length: number): string | null {
  if (max > 0 && base <= 0) {
    return `${label} burst maximum requires a base limit.`;
  }
  if (max > 0 && max < base) {
    return `${label} burst maximum must be at least the base limit.`;
  }
  if (length > 0 && max <= 0) {
    return `${label} burst length requires a burst maximum.`;
  }
  if (length > 0 && length < 1) {
    return `${label} burst length must be at least one second.`;
  }
  return null;
}

function familyBase(draft: ThrottleDraft, family: "bps" | "iops"): boolean {
  const values = familyValues(draft, family);
  if (values.mode === "total") {
    return number(values.total) > 0;
  }
  if (values.mode === "directional") {
    return number(values.read) > 0 || number(values.write) > 0;
  }
  return false;
}

function baseAndBursts(values: FamilyValues): Array<[string, string]> {
  if (values.mode === "total") {
    return [
      ["total", values.total],
      ["total maximum", values.totalMax],
      ["total burst length", values.totalMaxLength],
    ];
  }
  if (values.mode === "directional") {
    return [
      ["read", values.read],
      ["read maximum", values.readMax],
      ["read burst length", values.readMaxLength],
      ["write", values.write],
      ["write maximum", values.writeMax],
      ["write burst length", values.writeMaxLength],
    ];
  }
  return [];
}

function setValue(limits: DiskThrottleLimits, key: keyof DiskThrottleLimits, raw: string): void {
  const parsed = number(raw);
  if (parsed > 0) {
    limits[key] = parsed;
  }
}

function number(raw: string): number {
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? Math.trunc(parsed) : 0;
}
