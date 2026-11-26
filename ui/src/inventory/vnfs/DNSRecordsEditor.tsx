import type { DNSRecord, DNSRecordType } from "../types";

type DNSRecordsEditorProps = {
  records: DNSRecord[];
  disabled: boolean;
  onChange: (records: DNSRecord[]) => void;
};

/**
 * DNSRecordsEditor edits a DNS VNF's local records as independent child rows, with the supported
 * A/AAAA/CNAME/TXT/PTR types and the CNAME exclusivity rule left to the server's duplicate checks.
 */
export function DNSRecordsEditor({ records, disabled, onChange }: DNSRecordsEditorProps) {
  const update = <K extends keyof DNSRecord>(index: number, key: K, value: DNSRecord[K]) => {
    onChange(records.map((record, index_) => (index_ === index ? { ...record, [key]: value } : record)));
  };

  return (
    <div class="form-grid">
      <div class="field field-wide">
        <div class="panel-head acl-rules-head">
          <div>
            <span class="field-label">DNS records</span>
            <span class="field-hint">
              Names are served authoritatively. Use a fully qualified name such as vm1.lab1.test; a
              leading *. wildcard is supported. A CNAME must be the only record for its name and
              cannot coexist with other records.
            </span>
          </div>
          <button
            class="button ghost"
            type="button"
            disabled={disabled}
            onClick={() => onChange([...records, { name: "", type: "A", value: "", ttl: 300 }])}
          >
            Add record
          </button>
        </div>
        <div class="acl-rule-rows">
          {records.map((record, index) => (
            <div class="acl-rule" key={record.id ?? `new-${index}`}>
              <div class="acl-rule-head">
                <span class="dnat-rule-title">Record {index + 1}</span>
                <button
                  class="row-action danger"
                  type="button"
                  disabled={disabled}
                  onClick={() => onChange(records.filter((_, index_) => index_ !== index))}
                >
                  Remove
                </button>
              </div>
              <div class="acl-rule-fields">
                <label class="field">
                  <span class="field-label">Name</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder="vm1.lab1.test"
                    value={record.name}
                    onInput={(event) => update(index, "name", event.currentTarget.value)}
                  />
                </label>
                <label class="field">
                  <span class="field-label">Type</span>
                  <select
                    disabled={disabled}
                    value={record.type}
                    onChange={(event) =>
                      update(index, "type", event.currentTarget.value as DNSRecordType)
                    }
                  >
                    <option value="A">A</option>
                    <option value="AAAA">AAAA</option>
                    <option value="CNAME">CNAME</option>
                    <option value="TXT">TXT</option>
                    <option value="PTR">PTR</option>
                  </select>
                </label>
                <label class="field">
                  <span class="field-label">Value</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder={
                      record.type === "A"
                        ? "10.20.0.50"
                        : record.type === "AAAA"
                          ? "fd00:20::50"
                          : record.type === "CNAME" || record.type === "PTR"
                            ? "vm1.lab1.test"
                            : "text"
                    }
                    value={record.value}
                    onInput={(event) => update(index, "value", event.currentTarget.value)}
                  />
                </label>
                <label class="field">
                  <span class="field-label">TTL (seconds)</span>
                  <input
                    type="number"
                    min={1}
                    max={86400}
                    disabled={disabled}
                    placeholder="300"
                    value={record.ttl || ""}
                    onInput={(event) => update(index, "ttl", Number(event.currentTarget.value))}
                  />
                </label>
              </div>
            </div>
          ))}
          {records.length === 0 ? (
            <p class="field-hint">No local records yet. Add one, or use this VNF only as a forwarder.</p>
          ) : null}
        </div>
      </div>
    </div>
  );
}
