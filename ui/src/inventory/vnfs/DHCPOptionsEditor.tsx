/** Editor for the DHCP VNF option child rows. */
import type { DHCPOption } from "../types";

/** DHCPOptionFamily is the DHCP family whose option list is being edited. */
export type DHCPOptionFamily = "dhcpv4" | "dhcpv6";

type DHCPOptionPreset = {
  code: number;
  label: string;
  format: string;
  placeholder: string;
  hint?: string;
};

const DHCPV4_OPTION_PRESETS: DHCPOptionPreset[] = [
  { code: 3, label: "Router", format: "ipv4", placeholder: "10.20.0.1", hint: "Overrides the gateway router option." },
  { code: 6, label: "DNS servers", format: "ipv4", placeholder: "10.20.0.2, 1.1.1.1", hint: "Overrides the DNS field." },
  { code: 15, label: "Domain name", format: "string", placeholder: "lab.test", hint: "This is the domain name, not DNS server addresses. Use code 6 for DNS servers." },
  { code: 42, label: "NTP servers", format: "ipv4", placeholder: "10.20.0.1, 10.20.0.2" },
  { code: 44, label: "WINS servers", format: "ipv4", placeholder: "10.20.0.10" },
  { code: 66, label: "TFTP server name", format: "string", placeholder: "tftp.lab.test" },
  { code: 67, label: "Bootfile name", format: "string", placeholder: "pxelinux.0" },
  { code: 119, label: "Domain search list", format: "domain-list", placeholder: "lab.test, apps.lab.test" },
  { code: 252, label: "WPAD URL", format: "string", placeholder: "http://wpad.lab.test/wpad.dat" },
];

const DHCPV6_OPTION_PRESETS: DHCPOptionPreset[] = [
  { code: 21, label: "SIP domain names", format: "domain-list", placeholder: "sip.lab.test" },
  { code: 22, label: "SIP servers", format: "ipv6", placeholder: "fd00:20::10" },
  { code: 23, label: "DNS recursive name servers", format: "ipv6", placeholder: "fd00:20::2", hint: "Overrides the DNS field." },
  { code: 24, label: "Domain search list", format: "domain-list", placeholder: "lab.test, apps.lab.test" },
  { code: 31, label: "SNTP servers", format: "ipv6", placeholder: "fd00:20::10" },
  { code: 59, label: "Bootfile URL", format: "string", placeholder: "http://[fd00:20::1]/boot" },
  { code: 60, label: "Bootfile parameters", format: "string", placeholder: "console=ttyS0" },
];

type DHCPOptionsEditorProps = {
  options: DHCPOption[];
  family: DHCPOptionFamily;
  disabled: boolean;
  onChange: (options: DHCPOption[]) => void;
};

/**
 * dhcpOptionPresets returns the DHCPv6 preset definitions for dhcpv6 VNFs and the DHCPv4 presets
 * for every other type.
 */
function dhcpOptionPresets(family: DHCPOptionFamily): DHCPOptionPreset[] {
  return family === "dhcpv6" ? DHCPV6_OPTION_PRESETS : DHCPV4_OPTION_PRESETS;
}

/**
 * dhcpOptionPreset finds the preset definition for one DHCP option code and family, returning
 * undefined when the code has no preset.
 */
function dhcpOptionPreset(family: DHCPOptionFamily, code: number): DHCPOptionPreset | undefined {
  return dhcpOptionPresets(family).find((preset) => preset.code === code);
}

/**
 * DHCPOptionsEditor edits the extra DHCP options as independent child rows, keeping the
 * family-specific code range and applying the preset encoding when a known code is chosen.
 */
export function DHCPOptionsEditor({ options, family, disabled, onChange }: DHCPOptionsEditorProps) {
  const maxCode = family === "dhcpv6" ? 65535 : 254;
  const datalistID = `dhcp-option-codes-${family}`;

  const update = <K extends keyof DHCPOption>(index: number, key: K, value: DHCPOption[K]) => {
    onChange(options.map((option, index_) => (index_ === index ? { ...option, [key]: value } : option)));
  };

  const updateCode = (index: number, code: number) => {
    const preset = dhcpOptionPreset(family, code);
    onChange(
      options.map((option, index_) =>
        index_ === index ? { ...option, code, ...(preset ? { format: preset.format } : {}) } : option
      )
    );
  };

  return (
    <div class="form-grid">
      <div class="field field-wide">
        <div class="panel-head acl-rules-head">
          <div>
            <span class="field-label">Extra DHCP options <em>optional</em></span>
            <span class="field-hint">
              Advertise additional options such as option 15 (domain name), 42 (NTP), 119 (domain
              search), or DHCPv6 option 24 (domain search). Use <code>string</code>, <code>ipv4</code>,{" "}
              <code>ipv6</code>, <code>hex</code>, <code>uint8</code>, <code>uint16</code>,{" "}
              <code>uint32</code>, or <code>domain-list</code>. DNS server options are set through the
              DNS field. Each code must be unique in this VNF.
            </span>
          </div>
          <button
            class="button ghost"
            type="button"
            disabled={disabled}
            onClick={() => onChange([...options, { code: 0, format: "string", value: "" }])}
          >
            Add option
          </button>
        </div>
        <div class="acl-rule-rows">
          {options.map((option, index) => (
            <div class="acl-rule" key={option.id ?? `new-${index}`}>
              <div class="acl-rule-head">
                <span class="dnat-rule-title">Option {index + 1}</span>
                <button
                  class="row-action danger"
                  type="button"
                  disabled={disabled}
                  onClick={() => onChange(options.filter((_, index_) => index_ !== index))}
                >
                  Remove
                </button>
              </div>
              <div class="acl-rule-fields">
                <label class="field dhcp-option-code">
                  <span class="field-label">Code</span>
                  <input
                    type="number"
                    required
                    disabled={disabled}
                    min={1}
                    max={maxCode}
                    {...{ list: datalistID }}
                    value={option.code || ""}
                    onInput={(event) => updateCode(index, Number(event.currentTarget.value))}
                  />
                  {(() => {
                    const preset = dhcpOptionPreset(family, option.code);
                    return (
                      <span class="field-hint">
                        {preset ? `${preset.label}${preset.hint ? ` — ${preset.hint}` : ""}` : "Custom option code"}
                      </span>
                    );
                  })()}
                </label>
                <label class="field dhcp-option-format">
                  <span class="field-label">Format</span>
                  <select
                    disabled={disabled}
                    value={option.format}
                    onChange={(event) => update(index, "format", event.currentTarget.value)}
                  >
                    <option value="string">string</option>
                    <option value="ipv4">ipv4</option>
                    <option value="ipv6">ipv6</option>
                    <option value="hex">hex</option>
                    <option value="uint8">uint8</option>
                    <option value="uint16">uint16</option>
                    <option value="uint32">uint32</option>
                    <option value="domain-list">domain-list</option>
                  </select>
                </label>
                <label class="field dhcp-option-value">
                  <span class="field-label">Value</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder={
                      dhcpOptionPreset(family, option.code)?.placeholder ??
                      (option.format === "ipv4"
                        ? "10.20.0.1, 10.20.0.2"
                        : option.format === "ipv6"
                          ? "fd00:20::1"
                          : option.format === "domain-list"
                            ? "lab.test, apps.lab.test"
                            : option.format === "hex"
                              ? "01:02:ff"
                              : "lab.test")
                    }
                    value={option.value}
                    onInput={(event) => update(index, "value", event.currentTarget.value)}
                  />
                </label>
              </div>
            </div>
          ))}
          {options.length === 0 ? (
            <p class="field-hint">
              No extra options. Example: code 15, format string, value lab.test advertises the domain
              name. DNS and router options override the DNS field and gateway.
            </p>
          ) : null}
        </div>
        <datalist id={datalistID}>
          {dhcpOptionPresets(family).map((preset) => (
            <option value={preset.code} label={`${preset.label} (${preset.format})`} />
          ))}
        </datalist>
      </div>
    </div>
  );
}
