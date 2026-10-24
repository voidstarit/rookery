import { render } from "preact";
import { VMsTab } from "./VMsTab";

export function mountVMsTab(root: Element | null): void {
  if (root) {
    render(<VMsTab />, root);
  }
}
