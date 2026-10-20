import type { ACL, ACLRule, VirtualInterface } from "../types";

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

export function aclByName(acls: ACL[], name: string): ACL | undefined {
  const lower = name.toLowerCase();
  return acls.find((acl) => acl.name.toLowerCase() === lower);
}

export function aclAssignments(acl: ACL, interfaces: VirtualInterface[]): VirtualInterface[] {
  return interfaces.filter((iface) => {
    const names = [
      ...(iface.ingress_acls || (iface.ingress_acl_name ? [iface.ingress_acl_name] : [])),
      ...(iface.egress_acls || (iface.egress_acl_name ? [iface.egress_acl_name] : [])),
    ];
    return names.some((name) => name.toLowerCase() === acl.name.toLowerCase());
  });
}

export function defaultACLRule(): ACLRule {
  return {
    action: "permit",
    protocol: "any",
    source: "0.0.0.0/0",
    destination: "0.0.0.0/0",
  };
}
