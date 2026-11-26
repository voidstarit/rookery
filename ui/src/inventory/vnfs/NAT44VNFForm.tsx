/** Form for the NAT44 parent scalar fields on the VNF page. */
import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { HostInterface, NAT44VNF, Network } from "../types";
import { firstIPv4Host } from "./format";

/** NAT44VNFFormState is the editable NAT44 parent scalar form state. */
export type NAT44VNFFormState = {
  gateway: string;
  outsideInterface: string;
  outsideAddress: string;
  outsideGateway: string;
  nat44Pool: string;
};

type NAT44VNFFormProps = {
  state: NAT44VNFFormState;
  network: Network | undefined;
  editing: boolean;
  enabled: boolean;
  vppReady: boolean;
  disabled: boolean;
  onChange: (state: NAT44VNFFormState) => void;
  onEnabledChange: (enabled: boolean) => void;
};

/**
 * defaultNAT44VNFForm builds the NAT44 parent form state for a new or existing VNF, deriving the
 * default gateway from the selected network when creating.
 */
export function defaultNAT44VNFForm(vnf: NAT44VNF | null, network: Network | undefined): NAT44VNFFormState {
  return {
    gateway: vnf?.gateway ?? (firstIPv4Host(network?.ipv4_cidr) || "10.20.0.1/24"),
    outsideInterface: vnf?.outside_interface ?? "",
    outsideAddress: vnf?.outside_address ?? "",
    outsideGateway: vnf?.outside_gateway ?? "",
    nat44Pool: vnf?.nat44_pool ?? "",
  };
}

/**
 * outsideInterfaceHint describes the consequences of using a host interface as the NAT44 outside
 * uplink, warning when it carries the default route or is virtual, VPP-managed, or wireless.
 */
function outsideInterfaceHint(interfaces: HostInterface[], selected: string): string {
  const iface = interfaces.find((item) => item.name === selected);
  if (iface?.default_route) {
    return "This NIC carries the host default route and cannot be used for NAT.";
  }
  if (iface?.physical === false) {
    return "Select a wired physical NIC; virtual and VPP-managed interfaces are not valid outside uplinks.";
  }
  if (iface?.wireless) {
    return "Wireless NICs are not supported as VPP NAT44 outside uplinks; use a wired NIC.";
  }
  return iface
    ? `VPP will take over ${iface.name} and flush its host addresses.`
    : "The selected NIC will be taken over by VPP; its host addresses are flushed.";
}

/**
 * NAT44VNFForm renders the NAT44 parent scalar fields — gateway, outside uplink, pool, and enabled
 * toggle — and loads the candidate host interfaces from /api/host/interfaces.
 */
export function NAT44VNFForm({
  state,
  network,
  editing,
  enabled,
  vppReady,
  disabled,
  onChange,
  onEnabledChange,
}: NAT44VNFFormProps) {
  const [hostInterfaces, setHostInterfaces] = useState<HostInterface[]>([]);
  const [hostInterfacesLoading, setHostInterfacesLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setHostInterfacesLoading(true);
    api<HostInterface[]>("/api/host/interfaces")
      .then((items) => {
        if (!cancelled) {
          setHostInterfaces(Array.isArray(items) ? items : []);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setHostInterfaces([]);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setHostInterfacesLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const update = <K extends keyof NAT44VNFFormState>(key: K, value: NAT44VNFFormState[K]) => {
    onChange({ ...state, [key]: value });
  };

  return (
    <>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Gateway CIDR</span>
          <input
            type="text"
            required
            disabled={disabled}
            placeholder="10.20.0.1/24 (network BVI gateway)"
            autocomplete="off"
            spellcheck={false}
            value={state.gateway}
            onInput={(event) => update("gateway", event.currentTarget.value)}
          />
          <span class="field-hint">Network range: {network?.ipv4_cidr || "not set"}</span>
        </label>

        <label class="field">
          <span class="field-label">Outside NIC</span>
          <select
            required
            disabled={disabled}
            value={state.outsideInterface}
            onChange={(event) => update("outsideInterface", event.currentTarget.value)}
          >
            <option value="" disabled>
              {hostInterfacesLoading ? "Loading host interfaces…" : "Select a wired physical NIC…"}
            </option>
            {hostInterfaces.map((iface) => {
              const blocked = iface.default_route || iface.physical === false || iface.wireless;
              const addresses =
                Array.isArray(iface.addresses) && iface.addresses.length
                  ? ` — ${iface.addresses.join(", ")}`
                  : "";
              const reason = iface.default_route
                ? " (default route — blocked)"
                : iface.physical === false
                  ? " (virtual — not selectable)"
                  : iface.wireless
                    ? " (wireless — not supported)"
                    : "";
              return (
                <option value={iface.name} disabled={blocked} key={iface.name}>
                  {iface.name}
                  {addresses}
                  {reason}
                </option>
              );
            })}
            {state.outsideInterface &&
            !hostInterfaces.some((iface) => iface.name === state.outsideInterface) ? (
              <option value={state.outsideInterface}>
                {state.outsideInterface} (not currently present)
              </option>
            ) : null}
          </select>
          <span class="field-hint">{outsideInterfaceHint(hostInterfaces, state.outsideInterface)}</span>
        </label>

        <label class="field">
          <span class="field-label">Outside address <em>optional</em></span>
          <input
            type="text"
            disabled={disabled}
            placeholder="empty = VPP DHCP client"
            autocomplete="off"
            spellcheck={false}
            value={state.outsideAddress}
            onInput={(event) => update("outsideAddress", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Outside gateway <em>optional</em></span>
          <input
            type="text"
            disabled={disabled}
            placeholder="198.51.100.1"
            autocomplete="off"
            spellcheck={false}
            value={state.outsideGateway}
            onInput={(event) => update("outsideGateway", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">NAT44 pool <em>optional</em></span>
          <input
            type="text"
            disabled={disabled}
            placeholder="203.0.113.10-203.0.113.20"
            autocomplete="off"
            spellcheck={false}
            value={state.nat44Pool}
            onInput={(event) => update("nat44Pool", event.currentTarget.value)}
          />
        </label>
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
          {!vppReady
            ? "VPP is not ready. Save disabled and start the NAT44 VNF later."
            : enabled
              ? "Saving takes over the selected outside NIC and flushes its host addresses."
              : "The configuration is saved without taking over the outside NIC."}
        </span>
      </label>
    </>
  );
}
