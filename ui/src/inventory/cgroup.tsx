import type { CgroupIOLimit, HostBlockDevice } from "./types";

// parseCPUSet is deliberately forgiving: the text input is the source of truth
// while the user types, and only valid entries are reflected in the chip grid.
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

function bpsToMiB(value: number | undefined): string {
  if (!value) {
    return "";
  }
  return String(Math.round((value / MIB) * 10) / 10);
}

function mibToBps(value: string): number {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0;
  }
  return Math.round(parsed * MIB);
}

function numberValue(value: number | string | undefined): string {
  return value === undefined || value === 0 ? "" : String(value);
}

function parseNonNegative(value: string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
}

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
