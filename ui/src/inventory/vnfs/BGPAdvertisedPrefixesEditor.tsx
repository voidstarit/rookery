/** Editor for the BGP VNF advertised-prefix child rows. */
import type { BGPPrefix } from "../types";

type BGPAdvertisedPrefixesEditorProps = {
  prefixes: BGPPrefix[];
  disabled: boolean;
  onChange: (prefixes: BGPPrefix[]) => void;
};

/**
 * BGPAdvertisedPrefixesEditor edits a BGP VNF's advertised prefixes as independent child rows,
 * with one IPv4/IPv6 CIDR per row and a uniqueness check for the prefix.
 */
export function BGPAdvertisedPrefixesEditor({
  prefixes,
  disabled,
  onChange,
}: BGPAdvertisedPrefixesEditorProps) {
  const update = (index: number, prefix: string) => {
    onChange(prefixes.map((row, index_) => (index_ === index ? { ...row, prefix } : row)));
  };

  return (
    <div class="form-grid">
      <div class="field field-wide">
        <div class="panel-head acl-rules-head">
          <div>
            <span class="field-label">Advertised prefixes <em>optional</em></span>
            <span class="field-hint">
              One CIDR per row. These prefixes are advertised to the peer; each prefix must be
              unique. Adding or removing a prefix reloads the running BGP session configuration
              without restarting it.
            </span>
          </div>
          <button
            class="button ghost"
            type="button"
            disabled={disabled}
            onClick={() => onChange([...prefixes, { prefix: "" }])}
          >
            Add prefix
          </button>
        </div>
        <div class="acl-rule-rows">
          {prefixes.map((row, index) => (
            <div class="acl-rule" key={row.id ?? `new-${index}`}>
              <div class="acl-rule-head">
                <span class="dnat-rule-title">Prefix {index + 1}</span>
                <button
                  class="row-action danger"
                  type="button"
                  disabled={disabled}
                  onClick={() => onChange(prefixes.filter((_, index_) => index_ !== index))}
                >
                  Remove
                </button>
              </div>
              <div class="acl-rule-fields">
                <label class="field">
                  <span class="field-label">Prefix</span>
                  <input
                    type="text"
                    required
                    disabled={disabled}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder="10.10.0.0/16"
                    value={row.prefix}
                    onInput={(event) => update(index, event.currentTarget.value)}
                  />
                </label>
              </div>
            </div>
          ))}
          {prefixes.length === 0 ? (
            <p class="field-hint">No advertised prefixes. Received routes are still installed in the target table.</p>
          ) : null}
        </div>
      </div>
    </div>
  );
}
