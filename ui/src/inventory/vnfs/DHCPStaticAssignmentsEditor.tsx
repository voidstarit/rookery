import type { DHCPStaticAssignment } from "../types";

type DHCPStaticAssignmentsEditorProps = {
  assignments: DHCPStaticAssignment[];
  disabled: boolean;
  onChange: (assignments: DHCPStaticAssignment[]) => void;
};

/**
 * DHCPStaticAssignmentsEditor edits the DHCPv4 static reservations as independent child rows,
 * letting the user add, change, and remove MAC/IP pairs whose duplicates the server rejects.
 */
export function DHCPStaticAssignmentsEditor({
  assignments,
  disabled,
  onChange,
}: DHCPStaticAssignmentsEditorProps) {
  const update = <K extends keyof DHCPStaticAssignment>(
    index: number,
    key: K,
    value: DHCPStaticAssignment[K]
  ) => {
    onChange(assignments.map((assignment, index_) => (index_ === index ? { ...assignment, [key]: value } : assignment)));
  };

  return (
    <div class="form-grid">
      <div class="field field-wide">
        <div class="panel-head acl-rules-head">
          <div>
            <span class="field-label">Static assignments <em>optional</em></span>
            <span class="field-hint">
              Reserve an IPv4 address for a MAC address. Reserved addresses are excluded from the
              dynamic pool and can be inside or outside the DHCP range. Each MAC and IP must be
              unique in this VNF.
            </span>
          </div>
          <button
            class="button ghost"
            type="button"
            disabled={disabled}
            onClick={() => onChange([...assignments, { mac: "", ip: "" }])}
          >
            Add assignment
          </button>
        </div>
        <div class="acl-rule-rows">
          {assignments.map((assignment, index) => (
            <div class="acl-rule" key={assignment.id ?? `new-${index}`}>
              <div class="acl-rule-head">
                <span class="dnat-rule-title">Assignment {index + 1}</span>
                <button
                  class="row-action danger"
                  type="button"
                  disabled={disabled}
                  onClick={() => onChange(assignments.filter((_, index_) => index_ !== index))}
                >
                  Remove
                </button>
              </div>
              <div class="acl-rule-fields">
                <label class="field dhcp-static-field">
                  <span class="field-label">MAC address</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder="02:00:00:00:00:01"
                    value={assignment.mac}
                    onInput={(event) => update(index, "mac", event.currentTarget.value)}
                  />
                </label>
                <label class="field dhcp-static-field">
                  <span class="field-label">IP address</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    inputMode="decimal"
                    placeholder="10.20.0.50"
                    value={assignment.ip}
                    onInput={(event) => update(index, "ip", event.currentTarget.value)}
                  />
                </label>
              </div>
            </div>
          ))}
          {assignments.length === 0 ? (
            <p class="field-hint">No static assignments. Clients receive addresses from the dynamic range.</p>
          ) : null}
        </div>
      </div>
    </div>
  );
}
