/** Form for the DNS parent scalar fields on the VNF page. */
import type { DNSVNF } from "../types";

/** DNSVNFFormState is the editable DNS parent form state. */
export type DNSVNFFormState = {
  forward: boolean;
};

type DNSVNFFormProps = {
  state: DNSVNFFormState;
  editing: boolean;
  enabled: boolean;
  vppReady: boolean;
  disabled: boolean;
  onChange: (state: DNSVNFFormState) => void;
  onEnabledChange: (enabled: boolean) => void;
};

/**
 * defaultDNSVNFForm builds the DNS parent form state for a new or existing VNF, defaulting to
 * forwarding enabled and keeping the stored value when editing.
 */
export function defaultDNSVNFForm(vnf: DNSVNF | null): DNSVNFFormState {
  return { forward: vnf?.forward ?? true };
}

/**
 * DNSVNFForm renders the DNS parent scalar fields — forwarding and the enabled toggle — and
 * explains that local records and upstream forwarders are edited as independent child rows.
 */
export function DNSVNFForm({
  state,
  editing,
  enabled,
  vppReady,
  disabled,
  onChange,
  onEnabledChange,
}: DNSVNFFormProps) {
  return (
    <>
      <div class="form-grid">
        <label class="field field-wide checkbox-field">
          <span class="checkbox-row">
            <input
              type="checkbox"
              checked={state.forward}
              disabled={disabled}
              onChange={(event) => onChange({ ...state, forward: event.currentTarget.checked })}
            />
            <span>Forward names without a local record to upstream DNS servers</span>
          </span>
          <span class="field-hint">
            When disabled, unknown names receive NXDOMAIN. Local records always take precedence.
          </span>
        </label>
        <p class="field-hint field-wide">
          DHCPv4 clients can use the IPv4 .2 service address; DHCPv6 clients can use the IPv6 .2
          service address. Set that address in the DHCP VNF's DNS field, or configure it directly on
          clients.
        </p>
      </div>

      <label class="field field-wide checkbox-field">
        <span class="checkbox-row">
          <input
            type="checkbox"
            checked={enabled}
            disabled={disabled || (!editing && !vppReady)}
            onChange={(event) => onEnabledChange(event.currentTarget.checked)}
          />
          <span>Enable this VNF</span>
        </span>
        <span class="field-hint">
          {vppReady
            ? "Starts the DNS server on the network service address (.2, UDP/TCP 53) using the shared VNF memif."
            : "VPP is not ready. The DNS VNF can be created disabled and started later."}
        </span>
      </label>
    </>
  );
}
