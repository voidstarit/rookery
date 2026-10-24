import { render } from "preact";
import { ConsoleApp } from "./ConsoleApp";

const root = document.getElementById("console-root");
if (root) {
  render(<ConsoleApp />, root);
}
