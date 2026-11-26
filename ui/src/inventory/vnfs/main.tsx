import { render } from "preact";
import { VNFsTab } from "./VNFsTab";

/**
 * mountVNFsTab renders the VNFsTab component into the given root element and does nothing when the
 * root is null.
 */
export function mountVNFsTab(root: Element | null): void {
  if (root) {
    render(<VNFsTab />, root);
  }
}
