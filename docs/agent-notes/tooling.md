# Tooling notes

How agents should navigate and edit the two source languages in this repository. The goal is
symbol-aware tooling first, text search only for literals.

## Choosing a tool

| Task | Go | TypeScript / Preact |
| --- | --- | --- |
| Find a symbol by name | `gopls workspace_symbol` | Editor/agent language server workspace symbols |
| Outline a file | `gopls symbols <file>` | Language server document symbols |
| Definition / references | `gopls definition` / `gopls references` | Language server definition / references |
| Interface implementations | `gopls implementation` | Language server implementations |
| Call hierarchy | `gopls call_hierarchy` | Language server call hierarchy |
| Type hierarchy | Editor LSP (no CLI) | Language server type hierarchy |
| Diagnostics | `gopls check <file>`, `go vet` | `tsc --noEmit` (`npm run typecheck`) |
| Rename | `gopls rename -w`, reviewed with `git diff` | Language server rename, reviewed with `git diff` |
| String literals, config keys, errors, event names, SQL, docs | `rg` / grep | `rg` / grep |
| Edits | Editor/apply-patch | Editor/apply-patch |

Do not use `grep -rn SomeSymbol` to answer "where is this defined?" and do not use `sed`/`perl -pi`
to edit source. Text search cannot see methods, interface dispatch, generated code, or build-tag
variants, and scripted in-place edits bypass formatting and review.

## Reading protocol

Navigation tells you where a symbol is; only the body tells you what it does. Do both, in that
order, and only as far as the task needs.

- **Outline before reading.** For a source file longer than 200 lines, run `scripts/glance <file>`
  (or `gopls symbols <file>` directly) and pick the symbols the task actually touches.
- **Read ranges, not files.** Read the chosen symbols plus about ten lines of context. A `read`
  call with `offset`/`limit` costs a fraction of a whole-file read and keeps unrelated code out of
  context.
- **Read whole files deliberately.** Allowed when the file is 200 lines or fewer, or when the
  outline shows the task spans most of it; say which in one line so the choice is visible.
- **Never use `cat`/`head`/`tail`/`sed -n` on source.** They return no line numbers, ignore the
  read cap, and leave nothing for later range reads to anchor on.

`scripts/glance` prints gopls symbol names, kinds, and ranges for Go, and the top-level
declarations (types, functions, components, constants) for TypeScript/Preact. Each symbol carries
the first sentence of its Go doc comment or JSDoc block, so the outline also summarizes what the
symbol does; `--no-doc` drops the summaries. It retries with `/tmp` caches for sandboxed checkouts
and falls back to a text outline when gopls is missing:

```sh
./scripts/glance internal/api/nat44.go               # Go: symbol map with doc summaries
./scripts/glance ui/src/inventory/vnfs/VNFPage.tsx   # TS/Preact: declaration outline
./scripts/glance --no-doc internal/store/store.go    # compact map, no docs
```

## Go

Run gopls from the repository root so it loads the module. Positions are 1-based
`file.go:line:column`; copy the exact location from a `definition`/`references` result.

```sh
gopls workspace_symbol -matcher fuzzy 'CreateNetworkHostTap'
gopls symbols internal/api/host_taps.go
gopls definition internal/api/host_taps.go:70:20
gopls references internal/api/host_taps.go:70:20
gopls implementation internal/api/host_taps.go:70:20
gopls call_hierarchy internal/api/host_taps.go:70:20
gopls check internal/api/host_taps.go
go doc ./internal/api Server
```

Notes:

- The first query indexes the module and may take a while; subsequent queries reuse the cache.
- `gopls mcp -instructions` prints the MCP tool contract for agents that can attach an MCP server.
- If Go tooling reports a read-only cache (common in sandboxed agents: a gopls persistent-index
  error, or `go doc`/`go build` failing under `~/.cache`), point both caches at a writable directory:

  ```sh
  XDG_CACHE_HOME=/tmp/rookery-gopls \
  GOCACHE=/tmp/rookery-go-build \
  gopls workspace_symbol -matcher fuzzy 'Thing'
  ```

- Use `gopls rename -w` only after checking `git diff`; it is symbol-aware, unlike a text replace.
- `go test`, `go vet`, and `gofmt` remain the verification authority. gopls diagnostics supplement
  them; they do not replace tests.

## Go navigation details and limits

The [gopls navigation reference](https://go.dev/gopls/features/navigation) documents behaviors
worth knowing:

- `definition` also works on import paths, package clauses, `go:embed` filenames, doc links,
  `go:linkname`, labels, and `return` statements.
- `references` reflect only the build configuration used for the selected file. A query on
  `foo_windows.go` will not see references in `bar_linux.go`, so repeat with the relevant
  `GOOS`/`GOARCH` or build tags when changing cross-platform code. References on a `go.mod`
  `require` line return the imports of that module.
- Interface-method references include concrete implementations, and concrete-method references
  include the interface methods. An embedded field is reported only as a field reference, so jump
  to the type declaration to find type references.
- `implementation` on a concrete type returns the interfaces it satisfies; on an interface it
  returns implementors. Use the editor's type hierarchy for explicit supertypes (`typeDefinition`
  and type hierarchy have no CLI).
- `call_hierarchy` returns static incoming and outgoing calls; dynamic calls are not included, so
  follow up with `references` when changing function behavior.

## TypeScript and Preact

The UI uses the repository-local TypeScript compiler (`ui/node_modules/.bin/tsc`, currently
TypeScript 7) with `jsxImportSource: preact` and `strict`/`noUnused*` enabled.

```sh
cd ui
npm run typecheck                     # tsc --noEmit, project config
./node_modules/.bin/tsc --noEmit --pretty false
./node_modules/.bin/tsc --noEmit --listFiles --explainFiles   # module graph
```

- For definitions, references, document symbols, and rename, use the TypeScript language service
  built into your editor or agent. The TypeScript 7 native compiler ships no standalone symbol-query
  CLI, so terminal-only environments cannot get symbol navigation from `rg`; use the language
  server where available, otherwise `rg -w 'Name'` plus reading the file, and validate with `tsc`.
- `tsc --noEmit` is the authoritative type check; it is also the fastest way to find the call sites
  affected by a type change.
- Do not navigate into `internal/webui/static/build` for definitions. It is the generated bundle;
  edit `ui/src` and run `make ui-build`.
- `rookery:*` CustomEvent names and DOM ids/classes are string contracts, not symbols. Search them
  with `rg`; keep them in sync with the bridges listed in the UI note.
- Preact components and hooks behave like functions to the type checker; use the language service
  for prop/type references rather than inferring them from JSX text.

## Availability

```sh
command -v gopls || go install golang.org/x/tools/gopls@latest
test -x ui/node_modules/.bin/tsc || make ui-install
```

Editor/agent integrations should use their Go and TypeScript language servers directly; the
commands above are the terminal fallback when the agent has no LSP tool.

## Edits and fallbacks

- Prefer the editor/apply-patch tool for source changes. If a language server supports rename, use
  it and review the diff.
- `rg`, `grep`, and `sed -n` are acceptable for non-symbol text, logs, generated output, and
  read-only inspection. `sed -i`/`perl -pi` on source files is not.
- If gopls or the TypeScript language server is unavailable, say so in your reasoning and fall back
  to `rg` plus `read`; never claim symbol accuracy from text matches alone.
- Verify touched Go files with `gopls check` plus the relevant `go test` package, and touched UI
  files with `npm run typecheck` (or `make ui-build` when the bundle changes).
