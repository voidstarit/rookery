import type { VNF, VNFType } from "../types";
import type { BGPVNFFormState } from "./BGPVNFForm";
import type { DHCPVNFFormState } from "./DHCPVNFForm";
import type { DNSVNFFormState } from "./DNSVNFForm";
import type { NAT44VNFFormState } from "./NAT44VNFForm";

/**
 * FormState is the typed create/edit shell state used by VNFPage and the payload helpers.
 */
export type FormState = {
  type: VNFType;
  name: string;
  networkID: number;
  enabled: boolean;
  dhcp: DHCPVNFFormState;
  dns: DNSVNFFormState;
  nat44: NAT44VNFFormState;
  bgp: BGPVNFFormState;
};

/**
 * scalarPayload builds the type-specific parent scalar fields shared by create and update; it never
 * includes child arrays, identity, or the enabled transition.
 */
export function scalarPayload(form: FormState): Record<string, unknown> {
  switch (form.type) {
    case "dhcpv4":
    case "dhcpv6": {
      const dnsVNFID = Number(form.dhcp.dnsVNFID) || 0;
      return {
        gateway: form.dhcp.gateway.trim(),
        range_start: form.dhcp.rangeStart.trim(),
        range_end: form.dhcp.rangeEnd.trim(),
        lease_time: form.dhcp.leaseTime.trim() || "12h",
        dns: form.dhcp.dns.trim(),
        bind_ip: form.dhcp.bindIP.trim(),
        transport: form.dhcp.transport,
        // The server rejects a non-zero dns_ttl when no DNS VNF is selected.
        // Send the whole group so clearing an existing integration also works.
        dns_vnf_id: dnsVNFID,
        dns_domain: dnsVNFID ? form.dhcp.dnsDomain.trim() : "",
        dns_ttl: dnsVNFID ? Number(form.dhcp.dnsTTL) || 300 : 0,
      };
    }
    case "dns":
      return { forward: form.dns.forward };
    case "nat44":
      return {
        gateway: form.nat44.gateway.trim(),
        outside_interface: form.nat44.outsideInterface,
        outside_address: form.nat44.outsideAddress.trim(),
        outside_gateway: form.nat44.outsideGateway.trim(),
        nat44_pool: form.nat44.nat44Pool.trim(),
      };
    case "bgp":
      return {
        transport: form.bgp.transport,
        local_as: Number(form.bgp.localAS),
        router_id: form.bgp.routerID.trim(),
        peer_as: Number(form.bgp.peerAS),
        peer_ip: form.bgp.peerIP.trim(),
        local_ip: form.bgp.localIP.trim(),
        peer_gateway: form.bgp.peerGateway.trim(),
        multihop_ttl: Number(form.bgp.multihopTTL) || 64,
        passive: form.bgp.passive,
        target_table_id: Number(form.bgp.targetTableID) || 0,
        next_hop: form.bgp.nextHop.trim(),
        ...(form.bgp.transport === "tap" && form.bgp.password ? { password: form.bgp.password } : {}),
      };
  }
}

/**
 * createPayload wraps the parent scalars with identity and always creates the VNF disabled so the
 * child rows can be written before the single enabling start.
 */
export function createPayload(form: FormState): Record<string, unknown> {
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    network_id: form.networkID,
    enabled: false,
    ...scalarPayload(form),
  };
  if (form.type === "dhcpv4" || form.type === "dhcpv6") {
    payload.family = form.type;
  }
  return payload;
}

/**
 * sameScalarValue compares a stored scalar to a form value using the stored domain. Protojson omits
 * zero and empty scalars, so an absent stored value means "" for strings, false for booleans, and 0
 * for numbers; comparing the raw values would send an unchanged parent PUT on every edit.
 */
function sameScalarValue(stored: unknown, value: unknown): boolean {
  if (typeof value === "string") {
    return (stored ?? "") === value;
  }
  if (typeof value === "boolean") {
    return (stored ?? false) === value;
  }
  if (typeof value === "number") {
    return (stored ?? 0) === value;
  }
  return stored === value;
}

/**
 * updatePayload returns only the parent scalar fields that differ from the stored record; an
 * unchanged parent is skipped so it does not stop and start the runtime, and enabled is applied
 * separately after the child writes.
 */
export function updatePayload(form: FormState, base: VNF | null): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  const name = form.name.trim();
  if (!base || name !== base.name) {
    payload.name = name;
  }
  if (!base || base.type !== form.type) {
    return { ...payload, ...scalarPayload(form) };
  }
  const stored = base as unknown as Record<string, unknown>;
  for (const [key, value] of Object.entries(scalarPayload(form))) {
    if (!sameScalarValue(stored[key], value)) {
      payload[key] = value;
    }
  }
  return payload;
}

