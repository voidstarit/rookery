import { render } from "preact";
import { VNFsTab } from "./VNFsTab";

export function mountVNFsTab(root: Element | null): void {
  if (root) {
    render(<VNFsTab />, root);
  }
}
