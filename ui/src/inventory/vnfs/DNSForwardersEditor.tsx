/** Editor for the DNS VNF upstream-forwarder child rows. */
import type { DNSForwarder } from "../types";

type DNSForwardersEditorProps = {
  forwarders: DNSForwarder[];
  disabled: boolean;
  onChange: (forwarders: DNSForwarder[]) => void;
};

/**
 * DNSForwardersEditor edits a DNS VNF's upstream resolvers as independent child rows, with an
 * explicit position that orders them and a uniqueness check for the address.
 */
export function DNSForwardersEditor({ forwarders, disabled, onChange }: DNSForwardersEditorProps) {
  const update = <K extends keyof DNSForwarder>(index: number, key: K, value: DNSForwarder[K]) => {
    onChange(
      forwarders.map((forwarder, index_) => (index_ === index ? { ...forwarder, [key]: value } : forwarder))
    );
  };

  return (
    <div class="form-grid">
      <div class="field field-wide">
        <div class="panel-head acl-rules-head">
          <div>
            <span class="field-label">Upstream DNS servers <em>optional</em></span>
            <span class="field-hint">
              Resolvers used when forwarding is enabled. Comma-separated lists are not used here:
              add one row per IPv4/IPv6 address or IP:port. Lower positions are tried first; each
              address must be unique. Leave empty to use /etc/resolv.conf on the server.
            </span>
          </div>
          <button
            class="button ghost"
            type="button"
            disabled={disabled}
            onClick={() =>
              onChange([...forwarders, { address: "", position: forwarders.length }])
            }
          >
            Add forwarder
          </button>
        </div>
        <div class="acl-rule-rows">
          {forwarders.map((forwarder, index) => (
            <div class="acl-rule" key={forwarder.id ?? `new-${index}`}>
              <div class="acl-rule-head">
                <span class="dnat-rule-title">Forwarder {index + 1}</span>
                <button
                  class="row-action danger"
                  type="button"
                  disabled={disabled}
                  onClick={() => onChange(forwarders.filter((_, index_) => index_ !== index))}
                >
                  Remove
                </button>
              </div>
              <div class="acl-rule-fields">
                <label class="field">
                  <span class="field-label">Address</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder="1.1.1.1 or 8.8.8.8:53"
                    value={forwarder.address}
                    onInput={(event) => update(index, "address", event.currentTarget.value)}
                  />
                </label>
                <label class="field">
                  <span class="field-label">Position</span>
                  <input
                    type="number"
                    min={0}
                    max={1024}
                    disabled={disabled}
                    value={forwarder.position}
                    onInput={(event) => update(index, "position", Number(event.currentTarget.value))}
                  />
                </label>
              </div>
            </div>
          ))}
          {forwarders.length === 0 ? (
            <p class="field-hint">No upstream servers. Forwarded names fall back to /etc/resolv.conf.</p>
          ) : null}
        </div>
      </div>
    </div>
  );
}
