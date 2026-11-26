import { build } from "esbuild";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

// Bundle the payload tests to a temporary ESM file so Node can run the
// TypeScript module and its imports without a separate test toolchain.
const dir = await mkdtemp(join(tmpdir(), "rookery-ui-test-"));
const outfile = join(dir, "payload.test.mjs");
try {
  await build({
    entryPoints: ["test/payload.test.ts"],
    bundle: true,
    platform: "node",
    format: "esm",
    outfile,
    logLevel: "silent",
  });
  await import(pathToFileURL(outfile).href);
} finally {
  await rm(dir, { recursive: true, force: true });
}
