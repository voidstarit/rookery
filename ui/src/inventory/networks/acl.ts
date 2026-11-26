import type { ACL, ACLRule, VirtualInterface } from "../types";

/**
 * formatACLRuleText renders one ACL rule as "ACTION protocol source -> destination", appending
 * source and destination port ranges for tcp/udp rules that set them and a trailing description
 * when present; empty protocol, source, or destination fall back to any/0.0.0.0/0.
 */
export function formatACLRuleText(rule: ACLRule): string {
  const action = String(rule.action || "").toUpperCase();
  const protocol = String(rule.protocol || "any").toLowerCase();
  let text = `${action} ${protocol} ${rule.source || "0.0.0.0/0"} -> ${rule.destination || "0.0.0.0/0"}`;
  if (protocol === "tcp" || protocol === "udp") {
    const sourceStart = Number(rule.source_port_start) || 0;
    const sourceEnd = Number(rule.source_port_end) || 0;
    const destinationStart = Number(rule.destination_port_start) || 0;
    const destinationEnd = Number(rule.destination_port_end) || 0;
    if (sourceStart || sourceEnd) {
      text += ` sports ${sourceStart || "*"}-${sourceEnd || sourceStart || "*"}`;
    }
    if (destinationStart || destinationEnd) {
      text += ` dports ${destinationStart || "*"}-${destinationEnd || destinationStart || "*"}`;
    }
  }
  if (rule.description) {
    text += `  — ${rule.description}`;
  }
  return text;
}

/**
 * formatACLText builds a multi-line summary of an ACL with its name, title, description, and
 * numbered rules, noting "(no rules)" for an empty rule list and the implicit default DENY;
 * missing fields render as placeholders.
 */
export function formatACLText(
  acl: Pick<ACL, "name" | "title" | "description" | "rules">,
  label = "ACL"
): string {
  const lines = [
    `${label}: ${acl.name || "(unnamed)"}`,
    `Title: ${acl.title || "—"}`,
    `Description: ${acl.description || "—"}`,
    "Rules:",
  ];
  const rules = Array.isArray(acl.rules) ? acl.rules : [];
  if (rules.length === 0) {
    lines.push("  (no rules)");
  } else {
    rules.forEach((rule, index) => {
      lines.push(`  ${index + 1}. ${formatACLRuleText(rule)}`);
    });
  }
  lines.push("Default action: DENY (implicit)");
  return lines.join("\n");
}

/**
 * aclByName finds an ACL by name using a case-insensitive comparison, returning undefined when
 * no ACL matches.
 */
export function aclByName(acls: ACL[], name: string): ACL | undefined {
  const lower = name.toLowerCase();
  return acls.find((acl) => acl.name.toLowerCase() === lower);
}

/**
 * aclAssignments lists the interfaces that reference the given ACL in either direction,
 * comparing names case-insensitively and falling back to the legacy single ingress/egress ACL
 * name fields when the list fields are empty.
 */
export function aclAssignments(acl: ACL, interfaces: VirtualInterface[]): VirtualInterface[] {
  return interfaces.filter((iface) => {
    const names = [
      ...(iface.ingress_acls || (iface.ingress_acl_name ? [iface.ingress_acl_name] : [])),
      ...(iface.egress_acls || (iface.egress_acl_name ? [iface.egress_acl_name] : [])),
    ];
    return names.some((name) => name.toLowerCase() === acl.name.toLowerCase());
  });
}

/**
 * defaultACLRule returns a new permit-any rule from 0.0.0.0/0 to 0.0.0.0/0, the starting point
 * for an added ACL entry.
 */
export function defaultACLRule(): ACLRule {
  return {
    action: "permit",
    protocol: "any",
    source: "0.0.0.0/0",
    destination: "0.0.0.0/0",
  };
}
