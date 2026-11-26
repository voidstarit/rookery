import { render } from "preact";
import { DisksTab } from "./DisksTab";

/**
 * mountDisksTab mounts the DisksTab island into the supplied container and is a no-op when the
 * container is null.
 */
export function mountDisksTab(root: Element | null): void {
  if (root) {
    render(<DisksTab />, root);
  }
}
