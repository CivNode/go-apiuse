# go-apiuse

Offline index of dominant call patterns ("shapes") for Go stdlib and popular
package APIs. Part of the CivNode Training semantic engine.

Training uses the index to answer one question: *how is this API actually
called in production Go code?* The answer powers "Used in production"
sidebars, hover popovers, and kata grading hints. It is deterministic, has
no runtime dependency on an LLM, and does not execute user code.

## Status

`v0.1.0`. Usable public API, reference `tiny_index.bin` fixture, ingest
tool, full test suite. The full production index artifact is distributed
out-of-band (see "Artifact distribution" below).

## Install

```bash
go get github.com/CivNode/go-apiuse@v0.1.0
```

## Library usage

```go
import goapiuse "github.com/CivNode/go-apiuse"

idx, err := goapiuse.Load("/var/lib/civnode/apiuse/latest.bin")
if err != nil {
    return err
}
for _, u := range idx.Usage("context.WithTimeout", 3) {
    fmt.Printf("%.0f%%  %s\n", u.Frequency*100, u.Pattern)
}
```

### Public surface

```go
type Index struct { /* opaque */ }

func Load(path string) (*Index, error)
func LoadFromFS(fsys fs.FS, path string) (*Index, error)

func (i *Index) Usage(qualName string, topN int) []Usage
func (i *Index) Meta() Meta
func (i *Index) Len() int

type Usage struct {
    Pattern      string
    Frequency    float64
    ExampleRepos []string
}
```

`qualName` is the fully-qualified callee. Examples:

- `context.WithTimeout`
- `net/http.HandlerFunc`
- `sync.Mutex.Lock`
- `(builtin).len`

## Call shape

A shape is a compact, deterministic string describing how a call is
written. The goal is aggressive clustering: a corpus of 10 calls to a given
API typically collapses to three or fewer shapes.

Shapes encode three facts:

1. **Context.** How the result is consumed. `short-decl[2]`, `assign[2]`,
   `return`, `stmt`, `arg-of-call`, `defer`, `go`, `control-head`,
   `value-spec`, `composite-elt`, `operand`.
2. **Arity.** `args=N`; `variadic` is appended when the call uses `...`.
3. **Arg categories.** One compact token per argument. Type-informed when
   types are available: `context`, `duration`, `error`, `chan`, `map`,
   `slice`, `func`, `interface`, `int-const`, `float-const`,
   `string-const`, `bool-const`. Syntactic fallback when types are
   unavailable: `int-literal`, `string-literal`, `ident`, `nil`,
   `addr-of`, `mul-expr`, `composite-lit`, `func-lit`, ...

Example output for `ctx, cancel := context.WithTimeout(parent, 5*time.Second)`:

```
short-decl[2] | args=2 | context, duration
```

Identifier names are deliberately discarded. Package-qualified selector
literals keep the selector path since `time.Second` vs `http.StatusOK`
carries real semantic weight.

## Ingest tool

```bash
go install github.com/CivNode/go-apiuse/cmd/go-apiuse-ingest@v0.1.0

go-apiuse-ingest \
    -o index.bin \
    -source "stdlib + top-200 GitHub Go repos, 2026-04-24" \
    /srv/corpus/stdlib \
    /srv/corpus/third-party/...
```

The ingest uses `golang.org/x/tools/go/packages` with full type info
(`NeedTypes | NeedTypesInfo | NeedSyntax`). Packages that fail to
type-check are logged and skipped; the tool exits non-zero only if every
package in the corpus failed. This matters because real-world corpora
contain generated code, tagged-build files, and packages whose transitive
imports cannot be resolved in isolation; dropping those is far better than
failing the whole run.

## Artifact distribution

The full corpus index is much larger than is appropriate for a Git repo.
CivNode ships it as a static asset on `civnode-storage` (OVHcloud
Frankfurt):

```
s3://civnode-storage/apiuse/index-2026-04-24.bin
s3://civnode-storage/apiuse/latest.json
```

`latest.json` is a pointer of the form:

```json
{
  "version": "2026-04-24",
  "bin": "apiuse/index-2026-04-24.bin",
  "sha256": "...",
  "size_bytes": 48234112,
  "built_at": "2026-04-24T02:17:00Z",
  "source": "stdlib + top-200 GitHub Go repos"
}
```

### Release pipeline

1. Weekly scheduled job on HEXD runs the ingest against the mirrored
   corpus, producing `index-YYYY-MM-DD.bin`.
2. Verify the artifact loads and returns usages for known APIs as a smoke
   test.
3. Upload to `civnode-storage` under `apiuse/`.
4. Update `apiuse/latest.json` atomically.
5. CivNode's Training backend polls `latest.json` on startup plus once per
   day, downloads if the version differs, and caches the result locally.
6. The browser grabs the artifact once per release and keeps it in
   IndexedDB.

## Fixture for tests

`testdata/tiny_index.bin` ships a hand-built index covering ten snippets
across three APIs (`context.WithTimeout`, `net/http.HandlerFunc`,
`sync.Mutex.Lock`). Regenerate it with:

```bash
REGEN_TINY_INDEX=1 go test -run TestLoad_TinyIndex ./...
```

## Development

```bash
make test   # go test ./... -race -count=1
make lint   # gofumpt -l -d . + golangci-lint run ./...
make fuzz   # go test -fuzz=FuzzDecode .
make fmt    # gofumpt -w .
```

## Licence

Apache-2.0. See [LICENSE](./LICENSE).
