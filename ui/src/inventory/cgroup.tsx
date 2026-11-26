import type { CgroupIOLimit, HostBlockDevice } from "./types";

/**
 * parseCPUSet is deliberately forgiving: the text input is the source of truth while the user
 * types, and only valid entries are reflected in the chip grid. Accepts comma-separated numbers
 * and ranges such as "0-3,7", skips malformed or negative entries, caps a single range at 4096
 * CPUs, and returns the unique CPUs in ascending order.
 */
export function parseCPUSet(value: string | undefined): number[] {
  if (!value) {
    return [];
  }
  const cpus = new Set<number>();
  for (const raw of value.split(",")) {
    const part = raw.trim();
    if (!part) {
      continue;
    }
    const [startText, endText, hasRange] = part.split("-");
    const start = Number(startText);
    if (!Number.isInteger(start) || start < 0) {
      continue;
    }
    const end = hasRange ? Number(endText) : start;
    if (!Number.isInteger(end) || end < start) {
      continue;
    }
    for (let cpu = start; cpu <= end && cpu - start < 4096; cpu++) {
      cpus.add(cpu);
    }
  }
  return [...cpus].sort((a, b) => a - b);
}

/**
 * formatCPUSet renders a CPU list as compact comma-separated ranges, for example [0,1,2,3,7] as
 * "0-3,7". Duplicates, non-integers, and negative values are dropped first; an empty list yields
 * an empty string, which callers treat as "all CPUs".
 */
export function formatCPUSet(cpus: number[]): string {
  const sorted = [...new Set(cpus)].filter((cpu) => Number.isInteger(cpu) && cpu >= 0).sort((a, b) => a - b);
  const parts: string[] = [];
  for (let i = 0; i < sorted.length; ) {
    const start = sorted[i];
    let end = start;
    while (i + 1 < sorted.length && sorted[i + 1] === end + 1) {
      i++;
      end = sorted[i];
    }
    parts.push(start === end ? String(start) : `${start}-${end}`);
    i++;
  }
  return parts.join(",");
}

/**
 * toggleCPU adds or removes one CPU from a cpuset string and returns the renormalized text, so
 * selecting a chip rewrites the field into canonical ranges.
 */
export function toggleCPU(value: string | undefined, cpu: number): string {
  const selected = new Set(parseCPUSet(value));
  if (selected.has(cpu)) {
    selected.delete(cpu);
  } else {
    selected.add(cpu);
  }
  return formatCPUSet([...selected]);
}

type CPUSelectorProps = {
  value: string;
  options: number[];
  onChange: (value: string) => void;
  hint?: string;
  disabled?: boolean;
};

/**
 * CPUSelector renders a chip grid of allowed CPUs plus the free-form cpuset text field; clicking a
 * chip toggles it through onChange and typing edits the raw value. The grid is omitted when no
 * online CPUs are known, and an empty value means all CPUs.
 */
export function CPUSelector({ value, options, onChange, hint, disabled }: CPUSelectorProps) {
  const selected = new Set(parseCPUSet(value));
  return (
    <div class="cgroup-cpu-selector">
      {options.length > 0 ? (
        <div class="cpu-grid" role="group" aria-label="Allowed CPU cores">
          {options.map((cpu) => (
            <button
              key={cpu}
              type="button"
              class={selected.has(cpu) ? "cpu-chip selected" : "cpu-chip"}
              aria-pressed={selected.has(cpu)}
              disabled={disabled}
              onClick={() => onChange(toggleCPU(value, cpu))}
            >
              {cpu}
            </button>
          ))}
        </div>
      ) : null}
      <input
        type="text"
        value={value}
        disabled={disabled}
        placeholder="e.g. 0-3,7 (empty means all CPUs)"
        onInput={(event) => onChange(event.currentTarget.value)}
      />
      {hint ? <span class="field-hint">{hint}</span> : null}
    </div>
  );
}

type IOLimitEditorProps = {
  limits: CgroupIOLimit[];
  devices: HostBlockDevice[];
  onChange: (limits: CgroupIOLimit[]) => void;
  disabled?: boolean;
};

const MIB = 1024 * 1024;

/**
 * bpsToMiB converts a bytes-per-second limit into a MiB/s input value rounded to one decimal;
 * zero or undefined renders as an empty field so the placeholder marks the limit as unlimited.
 */
function bpsToMiB(value: number | undefined): string {
  if (!value) {
    return "";
  }
  return String(Math.round((value / MIB) * 10) / 10);
}

/**
 * mibToBps parses a MiB/s text field into whole bytes per second; blank, non-numeric, or
 * non-positive input becomes 0, meaning no limit.
 */
function mibToBps(value: string): number {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0;
  }
  return Math.round(parsed * MIB);
}

/**
 * numberValue renders an optional numeric field value, collapsing undefined and 0 to an empty
 * string so unset limits show their placeholder instead of a literal zero.
 */
function numberValue(value: number | string | undefined): string {
  return value === undefined || value === 0 ? "" : String(value);
}

/**
 * parseNonNegative parses an integer input, flooring positive finite numbers and returning 0 for
 * blank, invalid, or non-positive text.
 */
function parseNonNegative(value: string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
}

/**
 * IOLimitEditor renders an editable table of per-device I/O limits: each row picks a block device
 * and sets read/write bandwidth, IOPS, and weight, and the add button appends the first device not
 * already constrained. Devices already in use are disabled in other rows' selectors.
 */
export function IOLimitEditor({ limits, devices, onChange, disabled }: IOLimitEditorProps) {
  const byPath = new Map(devices.map((device) => [device.path, device]));
  const used = new Set(limits.map((limit) => limit.device));
  const available = devices.filter((device) => !used.has(device.path));
  const update = (index: number, patch: Partial<CgroupIOLimit>) => {
    onChange(limits.map((limit, current) => (current === index ? { ...limit, ...patch } : limit)));
  };
  const add = () => {
    const device = available[0];
    if (!device) {
      return;
    }
    onChange([...limits, { device: device.path }]);
  };
  const remove = (index: number) => {
    onChange(limits.filter((_, current) => current !== index));
  };

  return (
    <div class="cgroup-io-editor">
      <div class="table-wrap">
        <table class="cgroup-io-table">
          <thead>
            <tr>
              <th scope="col">Block device</th>
              <th scope="col">Read MB/s</th>
              <th scope="col">Write MB/s</th>
              <th scope="col">Read IOPS</th>
              <th scope="col">Write IOPS</th>
              <th scope="col">Weight</th>
              <th scope="col">
                <span class="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {limits.length === 0 ? (
              <tr>
                <td colspan={7} class="muted">
                  No per-device I/O limits. Add a block device to constrain it.
                </td>
              </tr>
            ) : (
              limits.map((limit, index) => (
                <tr key={limit.device}>
                  <td>
                    <select
                      value={limit.device}
                      disabled={disabled}
                      onChange={(event) => update(index, { device: event.currentTarget.value })}
                    >
                      {!byPath.has(limit.device) ? <option value={limit.device}>{limit.device}</option> : null}
                      {devices.map((device) => (
                        <option key={device.path} value={device.path} disabled={used.has(device.path) && device.path !== limit.device}>
                          {device.path}
                          {device.model ? ` · ${device.model}` : ""}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    <input
                      type="number"
                      min={0}
                      step="any"
                      disabled={disabled}
                      value={bpsToMiB(limit.read_bps)}
                      placeholder="unlimited"
                      onInput={(event) => update(index, { read_bps: mibToBps(event.currentTarget.value) })}
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      min={0}
                      step="any"
                      disabled={disabled}
                      value={bpsToMiB(limit.write_bps)}
                      placeholder="unlimited"
                      onInput={(event) => update(index, { write_bps: mibToBps(event.currentTarget.value) })}
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      min={0}
                      step={1}
                      disabled={disabled}
                      value={numberValue(limit.read_iops)}
                      placeholder="unlimited"
                      onInput={(event) => update(index, { read_iops: parseNonNegative(event.currentTarget.value) })}
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      min={0}
                      step={1}
                      disabled={disabled}
                      value={numberValue(limit.write_iops)}
                      placeholder="unlimited"
                      onInput={(event) => update(index, { write_iops: parseNonNegative(event.currentTarget.value) })}
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      min={0}
                      max={10000}
                      step={1}
                      disabled={disabled}
                      value={numberValue(limit.weight)}
                      placeholder="inherit"
                      onInput={(event) => update(index, { weight: parseNonNegative(event.currentTarget.value) })}
                    />
                  </td>
                  <td>
                    <button class="row-action danger" type="button" disabled={disabled} onClick={() => remove(index)}>
                      Remove
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      <div class="actions">
        <button class="button ghost" type="button" disabled={disabled || available.length === 0} onClick={add}>
          Add block device
        </button>
      </div>
    </div>
  );
}
