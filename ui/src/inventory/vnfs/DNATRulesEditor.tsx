import type { DNATRule } from "../types";

type DNATRulesEditorProps = {
  rules: DNATRule[];
  disabled: boolean;
  onChange: (rules: DNATRule[]) => void;
};

/**
 * DNATRulesEditor edits a NAT44 VNF's port-forwarding rules as independent child rows, with the
 * per-protocol external port uniqueness left to the server's duplicate checks.
 */
export function DNATRulesEditor({ rules, disabled, onChange }: DNATRulesEditorProps) {
  const update = <K extends keyof DNATRule>(index: number, key: K, value: DNATRule[K]) => {
    onChange(rules.map((rule, index_) => (index_ === index ? { ...rule, [key]: value } : rule)));
  };

  return (
    <div class="form-grid">
      <div class="field field-wide">
        <div class="panel-head acl-rules-head">
          <div>
            <span class="field-label">DNAT / port forwarding <em>optional</em></span>
            <span class="field-hint">
              Expose services inside this network on the NAT44 outside address. Each protocol and
              external port pair must be unique. A rule write re-applies the VNF's full rule set in
              place without stopping NAT44.
            </span>
          </div>
          <button
            class="button ghost"
            type="button"
            disabled={disabled}
            onClick={() =>
              onChange([
                ...rules,
                { protocol: "tcp", external_port: 8080, local_ip: "", local_port: 80 },
              ])
            }
          >
            Add rule
          </button>
        </div>
        <div class="acl-rule-rows">
          {rules.map((rule, index) => (
            <div class="acl-rule dnat-rule" key={rule.id ?? `new-${index}`}>
              <div class="acl-rule-head">
                <span class="dnat-rule-title">Rule {index + 1}</span>
                <button
                  class="row-action danger"
                  type="button"
                  disabled={disabled}
                  onClick={() => onChange(rules.filter((_, index_) => index_ !== index))}
                >
                  Remove
                </button>
              </div>
              <div class="acl-rule-fields">
                <label class="field dnat-field-protocol">
                  <span class="field-label">Protocol</span>
                  <select
                    disabled={disabled}
                    value={rule.protocol}
                    onChange={(event) =>
                      update(index, "protocol", event.currentTarget.value as "tcp" | "udp")
                    }
                  >
                    <option value="tcp">TCP</option>
                    <option value="udp">UDP</option>
                  </select>
                </label>
                <label class="field dnat-field-external-port">
                  <span class="field-label">External port</span>
                  <input
                    type="number"
                    min={1}
                    max={65535}
                    step={1}
                    required
                    disabled={disabled}
                    inputMode="numeric"
                    placeholder="8080"
                    value={rule.external_port || ""}
                    onInput={(event) => update(index, "external_port", Number(event.currentTarget.value))}
                  />
                </label>
                <label class="field dnat-field-local-ip">
                  <span class="field-label">Internal address</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    inputMode="decimal"
                    placeholder="10.20.0.100"
                    value={rule.local_ip}
                    onInput={(event) => update(index, "local_ip", event.currentTarget.value)}
                  />
                </label>
                <label class="field dnat-field-local-port">
                  <span class="field-label">Internal port</span>
                  <input
                    type="number"
                    min={1}
                    max={65535}
                    step={1}
                    required
                    disabled={disabled}
                    inputMode="numeric"
                    placeholder="80"
                    value={rule.local_port || ""}
                    onInput={(event) => update(index, "local_port", Number(event.currentTarget.value))}
                  />
                </label>
              </div>
            </div>
          ))}
          {rules.length === 0 ? (
            <p class="field-hint">No DNAT rules. NAT44 forwards outbound traffic without inbound mappings.</p>
          ) : null}
        </div>
      </div>
    </div>
  );
}
