import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { DHCPVNF, DNSVNFRecord, Network } from "../types";
import { firstIPv4Host, ipv4HostOffset, ipv6HostOffset, vnfTypeLabel } from "./format";

export type DHCPTransport = "" | "shared" | "dedicated" | "tap";

export type DHCPVNFFormState = {
  gateway: string;
  rangeStart: string;
  rangeEnd: string;
  leaseTime: string;
  dns: string;
  bindIP: string;
  transport: DHCPTransport;
  dnsVNFID: number;
  dnsDomain: string;
  dnsTTL: string;
};

type DHCPVNFFormProps = {
  state: DHCPVNFFormState;
  family: "dhcpv4" | "dhcpv6";
  network: Network | undefined;
  editing: boolean;
  enabled: boolean;
  vppReady: boolean;
  disabled: boolean;
  onChange: (state: DHCPVNFFormState) => void;
  onEnabledChange: (enabled: boolean) => void;
};

/**
 * normalizeDHCPTransport maps a stored transport to the form value, treating an empty string as
 * inheritance and a legacy memif as a dedicated memif; DHCPv6 only supports inherit or tap.
 */
function normalizeDHCPTransport(value: string | undefined, family: "dhcpv4" | "dhcpv6"): DHCPTransport {
  if (family === "dhcpv6") {
    return value === "tap" ? "tap" : "";
  }
  if (value === "shared" || value === "dedicated" || value === "tap") {
    return value;
  }
  return value === "memif" ? "dedicated" : "";
}

/**
 * defaultDHCPVNFForm builds the DHCP parent form state for a new or existing VNF, deriving the
 * gateway from the selected network and keeping stored scalar values when editing.
 */
export function defaultDHCPVNFForm(
  vnf: DHCPVNF | null,
  network: Network | undefined,
  family: "dhcpv4" | "dhcpv6"
): DHCPVNFFormState {
  const networkGateway = firstIPv4Host(network?.ipv4_cidr);
  return {
    gateway: vnf?.gateway ?? (family === "dhcpv6" ? "fd00:20::1/64" : networkGateway || "10.20.0.1/24"),
    rangeStart: vnf?.range_start ?? (family === "dhcpv6" ? "fd00:20::100" : "10.20.0.100"),
    rangeEnd: vnf?.range_end ?? (family === "dhcpv6" ? "fd00:20::200" : "10.20.0.200"),
    leaseTime: vnf?.lease_time ?? "12h",
    dns: vnf?.dns ?? (family === "dhcpv6" ? "2001:4860:4860::8888" : "1.1.1.1"),
    bindIP: vnf?.bind_ip ?? "",
    transport: normalizeDHCPTransport(vnf?.transport, family),
    dnsVNFID: vnf?.dns_vnf_id ?? 0,
    dnsDomain: vnf?.dns_domain ?? "",
    dnsTTL: String(vnf?.dns_ttl ?? 300),
  };
}

/**
 * DHCPVNFForm renders the DHCPv4/DHCPv6 parent scalar fields, including DNS integration, the
 * type-specific transport selector, and the enabled toggle; it fetches the DNS VNF choices for the
 * selected network from /api/dns-vnfs.
 */
export function DHCPVNFForm({
  state,
  family,
  network,
  editing,
  enabled,
  vppReady,
  disabled,
  onChange,
  onEnabledChange,
}: DHCPVNFFormProps) {
  const [dnsVNFs, setDnsVNFs] = useState<Array<{ id: number; name: string }>>([]);
  const networkID = network?.id ?? 0;
  const serviceIPv4 = ipv4HostOffset(network?.ipv4_cidr, 2);
  const serviceIPv6 = ipv6HostOffset(network?.ipv6_cidr, 2);
  const serviceAddress = family === "dhcpv6" ? serviceIPv6 : serviceIPv4;
  const sharedVNFAddress = (() => {
    const cidr = network?.ipv4_cidr?.trim();
    const host = ipv4HostOffset(cidr, 2);
    const bits = cidr?.split("/")[1];
    return host && bits ? `${host}/${bits}` : "";
  })();
  const dedicatedVNFAddress = state.gateway || "the gateway address";

  useEffect(() => {
    if (!networkID) {
      setDnsVNFs([]);
      return;
    }
    let cancelled = false;
    api<DNSVNFRecord[]>(`/api/dns-vnfs?network_id=${networkID}`)
      .then((records) => {
        if (!cancelled) {
          setDnsVNFs(
            (Array.isArray(records) ? records : []).map((record) => ({ id: record.id, name: record.name }))
          );
        }
      })
      .catch(() => {
        if (!cancelled) {
          setDnsVNFs([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [networkID]);

  const update = <K extends keyof DHCPVNFFormState>(key: K, value: DHCPVNFFormState[K]) => {
    onChange({ ...state, [key]: value });
  };

  return (
    <>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Family</span>
          <input type="text" readOnly value={vnfTypeLabel(family)} />
          <span class="field-hint">The family is fixed at creation and cannot be changed.</span>
        </label>

        <label class="field">
          <span class="field-label">Gateway CIDR</span>
          <input
            type="text"
            required
            disabled={disabled}
            placeholder={family === "dhcpv6" ? "fd00:20::1/64" : "10.20.0.1/24"}
            autocomplete="off"
            spellcheck={false}
            value={state.gateway}
            onInput={(event) => update("gateway", event.currentTarget.value)}
          />
          <span class="field-hint">Network range: {network?.ipv4_cidr || "not set"}</span>
        </label>

        <label class="field">
          <span class="field-label">DNS servers <em>optional</em></span>
          <input
            type="text"
            disabled={disabled}
            placeholder={family === "dhcpv6" ? "2001:4860:4860::8888" : "1.1.1.1,8.8.8.8"}
            autocomplete="off"
            spellcheck={false}
            value={state.dns}
            onInput={(event) => update("dns", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Range start</span>
          <input
            type="text"
            required
            disabled={disabled}
            placeholder={family === "dhcpv6" ? "fd00:20::100" : "10.20.0.100"}
            autocomplete="off"
            spellcheck={false}
            value={state.rangeStart}
            onInput={(event) => update("rangeStart", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Range end</span>
          <input
            type="text"
            required
            disabled={disabled}
            placeholder={family === "dhcpv6" ? "fd00:20::200" : "10.20.0.200"}
            autocomplete="off"
            spellcheck={false}
            value={state.rangeEnd}
            onInput={(event) => update("rangeEnd", event.currentTarget.value)}
          />
        </label>

        <label class="field">
          <span class="field-label">Lease time</span>
          <input
            type="text"
            disabled={disabled}
            placeholder="12h"
            autocomplete="off"
            spellcheck={false}
            value={state.leaseTime}
            onInput={(event) => update("leaseTime", event.currentTarget.value)}
          />
          <span class="field-hint">Duration such as 12h or 1h30m; legacy forms like 1h3 are accepted.</span>
        </label>

        <label class="field">
          <span class="field-label">Bind address <em>optional</em></span>
          <input
            type="text"
            disabled={disabled}
            placeholder={serviceAddress || "empty = network service address"}
            autocomplete="off"
            spellcheck={false}
            value={state.bindIP}
            onInput={(event) => update("bindIP", event.currentTarget.value)}
          />
          <span class="field-hint">Empty uses the network service address on the selected transport.</span>
        </label>

        <label class="field">
          <span class="field-label">VNF networking</span>
          <select
            disabled={disabled}
            value={state.transport}
            onChange={(event) => update("transport", event.currentTarget.value as DHCPTransport)}
          >
            <option value="">Inherit network default</option>
            {family === "dhcpv4" ? (
              <>
                <option value="shared">
                  Shared network memif{sharedVNFAddress ? ` · ${sharedVNFAddress}` : ""}
                </option>
                <option value="dedicated">
                  Dedicated memif{dedicatedVNFAddress ? ` · ${dedicatedVNFAddress}` : ""}
                </option>
              </>
            ) : null}
            <option value="tap">Tap{dedicatedVNFAddress ? ` · ${dedicatedVNFAddress}` : ""}</option>
          </select>
          <span class="field-hint">
            {state.transport === "shared"
              ? `The VNF uses the shared ${sharedVNFAddress || ".2 service"} address; the BVI owns the gateway (.1).`
              : state.transport === "dedicated"
                ? `The VNF uses ${dedicatedVNFAddress}; on a NAT network the server may move it to the .2 service address.`
                : state.transport === "tap"
                  ? `The tap is assigned ${dedicatedVNFAddress}; on a NAT network the server may move it to the .2 service address.`
                  : `Inherits the network default (${network?.vnf_transport || "shared"}).`}
          </span>
        </label>
      </div>

      <div class="form-grid">
        <label class="field field-wide">
          <span class="field-label">DHCP DNS integration <em>optional</em></span>
          <select
            disabled={disabled}
            value={String(state.dnsVNFID)}
            onChange={(event) => {
              const dnsVNFID = Number(event.currentTarget.value);
              onChange({
                ...state,
                dnsVNFID,
                ...(dnsVNFID > 0 && serviceAddress ? { dns: serviceAddress } : {}),
              });
            }}
          >
            <option value="0">No DNS integration</option>
            {dnsVNFs.map((candidate) => (
              <option value={String(candidate.id)} key={candidate.id}>
                {candidate.name}
              </option>
            ))}
          </select>
          <span class="field-hint">
            {dnsVNFs.length === 0
              ? "Create a DNS VNF on this network first to register DHCP leases."
              : "Active leases get A/AAAA and PTR records in the selected DNS VNF. The DNS server field is set to its service address (.2)."}
          </span>
        </label>
        {state.dnsVNFID > 0 ? (
          <>
            <label class="field">
              <span class="field-label">DNS domain</span>
              <input
                type="text"
                required
                disabled={disabled}
                autocomplete="off"
                spellcheck={false}
                placeholder="lab1.test"
                value={state.dnsDomain}
                onInput={(event) => update("dnsDomain", event.currentTarget.value)}
              />
              <span class="field-hint">Used when the client does not send a FQDN.</span>
            </label>
            <label class="field">
              <span class="field-label">DNS TTL (seconds)</span>
              <input
                type="number"
                min={0}
                max={86400}
                disabled={disabled}
                value={state.dnsTTL}
                onInput={(event) => update("dnsTTL", event.currentTarget.value)}
              />
            </label>
          </>
        ) : null}
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
            ? "Creates the VPP transport and starts the function immediately; the runtime starts once with every saved child row."
            : "VPP is not ready. The VNF can be created disabled and started later."}
        </span>
      </label>
    </>
  );
}
