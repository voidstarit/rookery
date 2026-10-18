import { render } from "preact";
import { MediaTab } from "./MediaTab";

export function mountMediaTab(root: Element | null): void {
  if (root) {
    render(<MediaTab />, root);
  }
}
