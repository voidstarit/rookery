import { render } from "preact";
import { NetworksTab } from "./NetworksTab";

/**
 * mountNetworksTab mounts the NetworksTab island into the supplied container and is a no-op when
 * the container is null.
 */
export function mountNetworksTab(root: Element | null): void {
  if (root) {
    render(<NetworksTab />, root);
  }
}
