import { render } from "preact";
import { DisksTab } from "./DisksTab";

export function mountDisksTab(root: Element | null): void {
  if (root) {
    render(<DisksTab />, root);
  }
}
