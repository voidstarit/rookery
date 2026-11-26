import type { BGPVNF } from "../types";

export type BGPVNFFormState = {
  transport: "memif" | "tap";
  localAS: string;
  routerID: string;
  peerAS: string;
  peerIP: string;
  localIP: string;
  peerGateway: string;
  multihopTTL: string;
  password: string;
  passive: boolean;
  targetTableID: string;
  nextHop: string;
};

type BGPVNFFormProps = {
  state: BGPVNFFormState;
  editing: boolean;
  enabled: boolean;
  vppReady: boolean;
  disabled: boolean;
  onChange: (state: BGPVNFFormState) => void;
  onEnabledChange: (enabled: boolean) => void;
};

/**
 * defaultBGPVNFForm builds the BGP parent form state for a new or existing VNF; the write-only
 * password always starts blank and is omitted on update unless the user types a new one.
 */
export function defaultBGPVNFForm(vnf: BGPVNF | null): BGPVNFFormState {
  return {
    transport: vnf?.transport === "tap" ? "tap" : "memif",
    localAS: String(vnf?.local_as ?? 64512),
    routerID: vnf?.router_id ?? "10.20.0.1",
    peerAS: String(vnf?.peer_as ?? 64513),
    peerIP: vnf?.peer_ip ?? "169.254.0.2",
    localIP: vnf?.local_ip ?? "169.254.0.1/30",
    peerGateway: vnf?.peer_gateway ?? "",
    multihopTTL: String(vnf?.multihop_ttl ?? 1),
    password: "",
    passive: vnf?.passive ?? false,
    targetTableID: String(vnf?.target_table_id ?? 0),
    nextHop: vnf?.next_hop ?? "",
  };
}

/**
 * BGPVNFForm renders the BGP parent scalar fields — transport, AS numbers, addresses, password,
 * session options, and the enabled toggle.
 */
export function BGPVNFForm({
  state,
  editing,
  enabled,
  vppReady,
  disabled,
  onChange,
  onEnabledChange,
}: BGPVNFFormProps) {
  const update = <K extends keyof BGPVNFFormState>(key: K, value: BGPVNFFormState[K]) => {
    onChange({ ...state, [key]: value });
  };

  return (
    <>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Transport</span>
          <select
            disabled={disabled}
            value={state.transport}
            onChange={(event) => update("transport", event.currentTarget.value as "memif" | "tap")}
          >
            <option value="memif">memif + gVisor</option>
            <option value="tap">VPP tap + kernel TCP</option>
          </select>
          <span class="field-hint">
            TCP MD5 passwords require the tap transport; memif/gVisor does not support TCP MD5.
          </span>
        </label>

        <label class="field checkbox-field">
          <span class="checkbox-row">
            <input
              type="checkbox"
              checked={state.passive}
              disabled={disabled}
              onChange={(event) => update("passive", event.currentTarget.checked)}
            />
            <span>Passive mode — wait for the peer to connect</span>
          </span>
        </label>

        <label class="field">
          <span class="field-label">Local AS</span>
          <input
            type="number"
            required
            min={1}
            max={4294967295}
            disabled={disabled}
            value={state.localAS}
            onInput={(event) => update("localAS", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Router ID</span>
          <input
            type="text"
            required
            disabled={disabled}
            placeholder="10.20.0.1"
            autocomplete="off"
            value={state.routerID}
            onInput={(event) => update("routerID", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Peer AS</span>
          <input
            type="number"
            required
            min={1}
            max={4294967295}
            disabled={disabled}
            value={state.peerAS}
            onInput={(event) => update("peerAS", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Peer IPv4/IPv6 address</span>
          <input
            type="text"
            required
            disabled={disabled}
            placeholder="169.254.0.2"
            autocomplete="off"
            value={state.peerIP}
            onInput={(event) => update("peerIP", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Local IPv4/IPv6 CIDR</span>
          <input
            type="text"
            required
            disabled={disabled}
            placeholder="169.254.0.1/30"
            autocomplete="off"
            value={state.localIP}
            onInput={(event) => update("localIP", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Peer gateway <em>optional</em></span>
          <input
            type="text"
            disabled={disabled}
            placeholder="169.254.0.2 (for routed/multihop peers)"
            autocomplete="off"
            value={state.peerGateway}
            onInput={(event) => update("peerGateway", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Advertised next hop <em>optional</em></span>
          <input
            type="text"
            disabled={disabled}
            placeholder="defaults to local_ip"
            autocomplete="off"
            value={state.nextHop}
            onInput={(event) => update("nextHop", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Multihop TTL</span>
          <input
            type="number"
            required
            min={1}
            max={255}
            disabled={disabled}
            value={state.multihopTTL}
            onInput={(event) => update("multihopTTL", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Target VPP table ID</span>
          <input
            type="number"
            required
            min={0}
            disabled={disabled}
            value={state.targetTableID}
            onInput={(event) => update("targetTableID", event.currentTarget.value)}
          />
          <span class="field-hint">0 is the default VRF; received IPv4 routes are installed here.</span>
        </label>

        <label class="field">
          <span class="field-label">TCP MD5 password <em>tap only</em></span>
          <input
            type="password"
            disabled={disabled || state.transport === "memif"}
            placeholder={state.transport === "memif" ? "Unavailable on memif" : editing ? "leave blank to keep" : "optional"}
            autocomplete="new-password"
            value={state.password}
            onInput={(event) => update("password", event.currentTarget.value)}
          />
          {editing ? (
            <span class="field-hint">Leave blank to keep the current password; the value is never returned by the API.</span>
          ) : null}
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
            ? "VPP is not ready. The BGP VNF can be created disabled and started later."
            : state.transport === "memif"
              ? "Creates a memif and starts the embedded BGP speaker on gVisor. TCP MD5 is unavailable on this transport."
              : "Creates a VPP tap and starts the embedded BGP speaker on kernel TCP; TCP MD5 is supported."}
        </span>
      </label>
    </>
  );
}
