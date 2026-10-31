import * as esbuild from "esbuild";

const watch = process.argv.includes("--watch");
const outdir = "../internal/webui/static/build";

const options = {
  entryPoints: {
    app: "src/inventory/main.ts",
    console: "src/console/main.tsx",
    lightvmConsole: "src/lightvm-console/main.tsx",
  },
  bundle: true,
  format: "esm",
  target: ["es2020"],
  outdir,
  jsx: "automatic",
  jsxImportSource: "preact",
  define: {
    "process.env.NODE_ENV": watch ? '"development"' : '"production"',
  },
  // noVNC stays vendored under static/novnc and is loaded by the browser.
  alias: {
    "novnc-rfb": "/novnc/core/rfb.js",
    "novnc-keysym": "/novnc/core/input/keysym.js",
  },
  external: ["/novnc/*"],
  minify: !watch,
  sourcemap: watch ? "inline" : false,
  legalComments: "none",
  loader: {
    ".woff": "file",
    ".woff2": "file",
  },
  assetNames: "fonts/[name]-[hash]",
  logLevel: "info",
};

if (watch) {
  const context = await esbuild.context(options);
  await context.watch();
  console.log("watching UI sources…");
} else {
  await esbuild.build(options);
}
