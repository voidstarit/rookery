import { render } from "preact";
import { NetworksTab } from "./NetworksTab";

export function mountNetworksTab(root: Element | null): void {
  if (root) {
    render(<NetworksTab />, root);
  }
}
