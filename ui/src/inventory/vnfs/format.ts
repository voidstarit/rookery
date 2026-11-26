import type {
  BGPVNF,
  BGPVNFRecord,
  DHCPVNF,
  DHCPVNFRecord,
  DNSVNF,
  DNSVNFRecord,
  NAT44VNF,
  NAT44VNFRecord,
  VNF,
  VNFType,
} from "../types";

/**
 * formatDate renders an ISO timestamp as a locale medium date and short time, returning an em dash
 * when the value is missing or unparseable.
 */
export function formatDate(value: string | undefined): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  return date.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

/**
 * vnfAPISlug returns the parent API path segment for a VNF type, e.g. dhcp-vnfs for dhcpv4.
 */
export function vnfAPISlug(type: VNFType): string {
  switch (type) {
    case "dhcpv4":
    case "dhcpv6":
      return "dhcp-vnfs";
    case "dns":
      return "dns-vnfs";
    case "nat44":
      return "nat44-vnfs";
    case "bgp":
      return "bgp-vnfs";
  }
}

/**
 * vnfTypeLabel returns the short human label for a VNF type used in the merged table and forms.
 */
export function vnfTypeLabel(type: VNFType): string {
  switch (type) {
    case "dhcpv4":
      return "DHCPv4";
    case "dhcpv6":
      return "DHCPv6";
    case "dns":
      return "DNS";
    case "nat44":
      return "NAT44";
    case "bgp":
      return "BGP";
  }
}

/**
 * tagDHCPVNF adds the family type tag to one DHCP API record.
 */
export function tagDHCPVNF(record: DHCPVNFRecord): DHCPVNF {
  return { ...record, type: record.family === "dhcpv6" ? "dhcpv6" : "dhcpv4" };
}

/**
 * tagDNSVNF adds the DNS type tag to one DNS API record.
 */
export function tagDNSVNF(record: DNSVNFRecord): DNSVNF {
  return { ...record, type: "dns" };
}

/**
 * tagNAT44VNF adds the NAT44 type tag to one NAT44 API record.
 */
export function tagNAT44VNF(record: NAT44VNFRecord): NAT44VNF {
  return { ...record, type: "nat44" };
}

/**
 * tagBGPVNF adds the BGP type tag to one BGP API record.
 */
export function tagBGPVNF(record: BGPVNFRecord): BGPVNF {
  return { ...record, type: "bgp" };
}

/**
 * sortVNFs orders a merged VNF list by name, with the record ID as a stable tie-breaker.
 */
export function sortVNFs(list: VNF[]): VNF[] {
  return [...list].sort(
    (left, right) =>
      left.name.localeCompare(right.name, undefined, { sensitivity: "base" }) || left.id - right.id
  );
}

/**
 * mergeVNFs tags the four typed lists, concatenates them, and sorts the result by name, giving the
 * single table a stable cross-type ordering.
 */
export function mergeVNFs(
  dhcp: DHCPVNFRecord[] = [],
  dns: DNSVNFRecord[] = [],
  nat44: NAT44VNFRecord[] = [],
  bgp: BGPVNFRecord[] = []
): VNF[] {
  return sortVNFs([
    ...dhcp.map(tagDHCPVNF),
    ...dns.map(tagDNSVNF),
    ...nat44.map(tagNAT44VNF),
    ...bgp.map(tagBGPVNF),
  ]);
}

/**
 * vnfSummary returns the type-specific detail line shown under a VNF name in the merged table.
 */
export function vnfSummary(vnf: VNF): string {
  switch (vnf.type) {
    case "dhcpv4":
    case "dhcpv6": {
      const parts: string[] = [];
      const staticCount = vnf.static_assignment_count ?? 0;
      const optionCount = vnf.option_count ?? 0;
      if (vnf.tap_host_name) {
        parts.push(vnf.tap_host_name);
      }
      if (vnf.type === "dhcpv4" && staticCount > 0) {
        parts.push(`${staticCount} static`);
      }
      if (optionCount > 0) {
        parts.push(`${optionCount} option${optionCount === 1 ? "" : "s"}`);
      }
      return parts.join(" · ") || vnfTypeLabel(vnf.type);
    }
    case "dns": {
      // protojson omits zero-valued scalars, so a zero count arrives as undefined.
      const recordCount = vnf.record_count ?? 0;
      const forwarderCount = vnf.forwarder_count ?? 0;
      const parts = [
        `${recordCount} record${recordCount === 1 ? "" : "s"}`,
        vnf.forward ? "forwarding" : "authoritative",
      ];
      if (forwarderCount > 0) {
        parts.push(`${forwarderCount} forwarder${forwarderCount === 1 ? "" : "s"}`);
      }
      return parts.join(" · ");
    }
    case "nat44": {
      const parts = [`outside ${vnf.outside_interface || "—"}`];
      const dnatRuleCount = vnf.dnat_rule_count ?? 0;
      if (dnatRuleCount > 0) {
        parts.push(`${dnatRuleCount} DNAT`);
      }
      return parts.join(" · ");
    }
    case "bgp": {
      const parts = [`AS${vnf.local_as} → AS${vnf.peer_as}`];
      const prefixCount = vnf.advertised_prefix_count ?? 0;
      if (prefixCount > 0) {
        parts.push(`${prefixCount} prefix${prefixCount === 1 ? "" : "es"}`);
      }
      return parts.join(" · ");
    }
  }
}

/**
 * vnfLeaseCount returns the active lease count for DHCP VNFs and undefined for every other type.
 */
export function vnfLeaseCount(vnf: VNF): number | undefined {
  // A zero lease_count is omitted by protojson; DHCP VNFs still have a count.
  return vnf.type === "dhcpv4" || vnf.type === "dhcpv6" ? (vnf.lease_count ?? 0) : undefined;
}

/**
 * ipv4HostOffset adds a fixed offset to the IPv4 address in a CIDR string and returns the dotted
 * quad, or an empty string when the input is missing or malformed.
 */
export function ipv4HostOffset(cidr: string | undefined, offset: number): string {
  if (!cidr) {
    return "";
  }
  const [addr, bits] = cidr.trim().split("/");
  const parts = addr.split(".").map(Number);
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255) || !bits) {
    return "";
  }
  const value = ((((parts[0] << 24) >>> 0) + (parts[1] << 16) + (parts[2] << 8) + parts[3] + offset) >>> 0);
  return `${(value >>> 24) & 255}.${(value >>> 16) & 255}.${(value >>> 8) & 255}.${value & 255}`;
}

/**
 * ipv6HostOffset adds a fixed offset to the IPv6 address in a CIDR string using BigInt arithmetic
 * and returns the full eight-group hex form, or an empty string when the address is missing,
 * invalid, or has too many groups.
 */
export function ipv6HostOffset(cidr: string | undefined, offset: number): string {
  if (!cidr) {
    return "";
  }
  const [addr, bits] = cidr.trim().split("/");
  if (!addr || !bits) {
    return "";
  }
  const halves = addr.split("::");
  if (halves.length > 2) {
    return "";
  }
  const parse = (value: string) => (value ? value.split(":") : []);
  const left = parse(halves[0]);
  const right = parse(halves[1] ?? "");
  const missing = 8 - left.length - right.length;
  if (missing < 0) {
    return "";
  }
  const groups = [...left, ...Array.from({ length: missing }, () => "0"), ...right];
  if (groups.some((group) => !/^[0-9a-fA-F]{1,4}$/.test(group))) {
    return "";
  }
  let value = 0n;
  for (const group of groups) {
    value = (value << 16n) + BigInt(parseInt(group, 16));
  }
  value += BigInt(offset);
  const output: string[] = [];
  for (let index = 0; index < 8; index += 1) {
    output.unshift((value & 0xffffn).toString(16));
    value >>= 16n;
  }
  return output.join(":");
}

/**
 * firstIPv4Host returns the first usable IPv4 address of a network CIDR with its prefix length
 * preserved, or an empty string when the CIDR is absent or unparseable.
 */
export function firstIPv4Host(cidr: string | undefined): string {
  const host = ipv4HostOffset(cidr, 1);
  if (!host || !cidr) {
    return "";
  }
  return `${host}/${cidr.trim().split("/")[1]}`;
}
