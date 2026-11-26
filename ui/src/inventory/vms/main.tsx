import { render } from "preact";
import { VMsTab } from "./VMsTab";

/**
 * mountVMsTab mounts the VMsTab island into the supplied container and is a no-op when the
 * container is null.
 */
export function mountVMsTab(root: Element | null): void {
  if (root) {
    render(<VMsTab />, root);
  }
}
