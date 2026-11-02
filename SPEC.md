# tether: specification

tether connects a Go HTTP API to a TypeScript frontend. You write handlers
and types in Go once; tether serves them, validates their input, and
generates an OpenAPI document and a fully typed TypeScript client with the
same validation rules.

This document is the binding reference for behaviour. The README is the
short introduction.

## 1. Overview

```go
//go:generate go tool tether -out web/src/api

type TaskInput struct {
	Title  string `json:"title"  v:"required,max=120"`
	Status Status `json:"status" v:"required"`
}

// CreateTask adds a task.
func CreateTask(r *http.Request, in TaskInput) (Task, error) { … }

mux.Handle(tether.Route("POST /v1/tasks", CreateTask))
```

```ts
import { createTask, taskInputSchema } from "./api";

const task = await createTask({ title: "Write spec", status: "open" });
```

tether has three parts, all in one Go module, `github.com/maulai/tether`:

| Part | What it does | Dependencies |
|---|---|---|
| package `tether` | reads requests, validates, writes responses and errors | standard library only |
| command `tether` (`go tool tether`) | reads the Go source, writes OpenAPI, TypeScript and one small Go file per package | `golang.org/x/tools` |
| TypeScript runtime | fetch, errors, validation; written into the output directory | none |

## 2. Principles

1. **Go is the only source.** Generated files say `DO NOT EDIT`. To change
   the TypeScript, change the Go.
2. **No project knowledge.** tether knows no field names, no domain formats,
   no directories. Anything project specific is registered by the project.
3. **Deterministic output.** Two runs over the same source produce
   identical bytes.
4. **Loud failures.** Anything tether cannot map exactly is an error when
   generating, never silently wrong output.
5. **Standalone.** The server library uses only the standard library. The
   generated TypeScript has no dependencies.
6. **Same rule, same result, same text.** Server and browser apply each
   rule identically and report it with the same message. Shared test
   vectors check both implementations.

## 3. Server

### 3.1 Routes

```go
func Route(pattern string, handler any) (string, http.Handler)
```

`pattern` is a `net/http.ServeMux` pattern with a method, for example
`"PUT /v1/tasks/{id}"`. It must be a string constant, because the generator
reads it from the source. Supported methods: `GET`, `POST`, `PUT`, `PATCH`,
`DELETE`.

The return values fit `mux.Handle` directly. Middleware wraps the handler as
usual:

```go
mux.Handle(tether.Route("GET /v1/tasks", ListTasks))

pattern, h := tether.Route("DELETE /v1/tasks/{id}", DeleteTask)
mux.Handle(pattern, requireAdmin(h))
```

Other routers work if they set path values through `Request.SetPathValue`.

### 3.2 Handlers

A handler is a **named function or method** with one of four shapes:

```go
func(r *http.Request) (Out, error)
func(r *http.Request, in In) (Out, error)
func(r *http.Request) error
func(r *http.Request, in In) error
```

`Route` panics at startup for any other shape. `In` must be a named,
non-generic struct type (or `tether.Upload`).

- **Name:** the TypeScript function is named after the handler, with its
  leading capitals lowercased (`CreateTask` becomes `createTask`, `HTTPGet`
  becomes `httpGet`). Two handlers with the same name, or a name that is a
  reserved word in TypeScript (`Delete`), are an error that names the place.
- **Description:** the handler's doc comment becomes the operation's
  description in OpenAPI and the doc comment of the TypeScript function.
- **Path parameters:** read with `r.PathValue("id")`, or parsed with
  `tether.PathValue[T](r, "id")` (3.8). The generator takes their names
  from the pattern; in TypeScript they are `string` arguments.

### 3.3 Input

Where the input comes from depends on the method:

| Method | `In` is read from |
|---|---|
| `GET`, `DELETE` | the query string; the key is the field's `json` name |
| `POST`, `PUT`, `PATCH` | the JSON body |

Body rules:
- Exactly one JSON value. An empty body decodes to the zero value, then
  validation runs.
- Unknown fields are rejected (`unknown`).
- A wrong JSON type is a field error (`type`, or `integer` for a fraction
  sent to an integer field). All such problems are reported at once, each
  with its exact path; validation runs only when there are none.
- `null` leaves the zero value, as with `encoding/json`.
- For a number or a `time.Time`, `""` means "not given": forms send it for
  an empty input.
- A `time.Time` accepts RFC 3339 and a plain date (`2026-11-01`, midnight
  UTC), which is what a date input yields.
- A JSON body is limited to `tether.MaxBodyBytes` (default 1 MiB). Real
  input is far smaller, and without a limit a single request could exhaust
  the server's memory. Uploads do not count against it (see below).

Query rules:
- Supported field types: strings, named string types, booleans, numbers,
  `time.Time`, pointers to these, and slices of these (the key repeated
  once per element, in order).
- Unknown keys are rejected (`unknown`). A value that does not parse is a
  field error (`type`). An empty single value means "not given".
- In a slice an empty value is an element: `tags=&tags=b` is `["", "b"]`,
  so positions hold and the element's rules apply. For a number or a
  pointer element it is the zero value or nil, as `null` is in JSON. An
  empty list sends no key and is the same as a missing one.
- A route whose handler takes no input, and a route that reads its input
  from the body, accept no query parameters at all: a parameter the server
  ignores looks like a filter that worked. A handler without input ignores
  the body.

**Uploads.** If `In` is `tether.Upload`, the body is `multipart/form-data`
with one file in the part `file`; other parts are skipped:

```go
type Upload struct {
	Name        string
	ContentType string
	Body        io.Reader // valid until the handler returns
}
```

The file is streamed to the handler, never buffered by tether; how large a
file may be is up to the handler and wherever it stores the file.

One request carries one file. To upload many, send one request per file,
in parallel: each file gets its own progress and retry, and one failure does
not lose the rest. Metadata belongs in a separate JSON request that refers to
the uploaded files by ID; that request is also where a set of files is made
to count together.

### 3.4 Output

| Handler returns | Response |
|---|---|
| `Out`, nil error, `GET`, `PUT`, `PATCH`, `DELETE` | `200` with `Out` as JSON |
| `Out`, nil error, `POST` | `201` with `Out` as JSON |
| only `error`, nil | `204` without body |
| `tether.File` | `200` with the raw bytes; `206`, `304` or `416` for range and conditional requests (see below) |
| non-nil error | see 3.5 |

- A nil slice is written as `[]` and a nil map as `{}`, so the TypeScript
  type never needs `| null` for them.
- `Out` may be a pointer; it is typed `T | null`.
- There is no envelope. `Out` is the whole body.

```go
type File struct {
	Name        string    // the file name in Content-Disposition
	ContentType string    // default application/octet-stream
	Body        io.Reader // closed after writing if it is an io.Closer
	ModTime     time.Time // enables Last-Modified and If-Modified-Since
	Attachment  bool      // download instead of showing it
}
```

**Disposition.** Without `Attachment` the file is shown (`inline`), with it
the browser downloads it (`attachment`); `Name` is added as the file name,
encoded per RFC 2231 when it is not plain ASCII (`filename*=utf-8''...`). A
file with neither sends no `Content-Disposition`.

**Ranges and conditions.** For `GET` and `HEAD`, a `Body` that is an
`io.ReadSeeker` is served with `http.ServeContent`, which brings from the
standard library: `Range` with 206 and `Content-Range`, 416 for a range
outside the file, `If-Range`, `Accept-Ranges`, `Content-Length`,
`If-Modified-Since` with 304 when `ModTime` is set, and `If-None-Match` with
304 when the handler set an `ETag` (3.7). Any other body, and any other
method, is streamed whole with 200. `HEAD` needs no pattern of its own:
`ServeMux` sends it to the `GET` pattern, and the body is left out; a body
that cannot seek is closed without being read, so a file produced while it
is read (an export through a pipe) stops early. The errors `ServeContent`
answers itself become problem documents like every other error: 416 with
message `range` and its `Content-Range` header, 500 with message
`internal`. A failed seek gives 500, or 416 inside a range, as
`ServeContent` decides; the seeker's message never reaches the client, and
with a 500 it goes to the log.

A nil pointer of a concrete error type inside the `error` result (a
classic Go mistake when a function returns `*MyError`) counts as no error.

### 3.5 Errors

Errors are written as RFC 9457 problem details
(`application/problem+json`), with an `errors` member for field errors:

```json
{
  "type": "about:blank",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "Please check the marked fields.",
  "errors": [
    { "field": "username", "rule": "required", "message": "Required." }
  ]
}
```

`field` is the path in the JSON input, with dots and numeric indices:
`items.2.sku`, `labels.de`. This is the notation form libraries such as
react-hook-form use, so it can be passed to them directly.

Handlers create errors with:

```go
func FieldError(field, message string) error // 422, one field
func Fail(status int, message string) error  // any status, no field
```

Several field errors are combined with the standard `errors.Join`.

| Error | Status | `detail` |
|---|---|---|
| validation or `FieldError` | 422 | message `invalid` |
| body is not valid JSON | 400 | message `malformed` |
| body too large | 413 | message `tooLarge` |
| `Fail(status, message)` | `status` | `message` |
| error with method `HTTPStatus() int` below 500 | that status | `err.Error()` |
| any other error, or status 500 and above | 500 or that status | message `internal`; the error is logged with `log/slog` and not sent |

A project needs no error type of its own: `FieldError` and `Fail` cover
every status (use the `net/http` constants), and any other error is answered
with 500 and logged, which keeps technical causes out of responses.
`HTTPStatus() int` exists only for code that must not import tether.

**Mapping errors.** Errors of packages a project cannot change, such as a
database driver's "no rows", are translated in one place instead of at
every `return`:

```go
func MapErrors(translate func(error) error)
```

Every non-nil error a handler returns passes through `translate` before the
table above applies. Returning the error unchanged keeps the default;
returning nil means the original error, so a mapping can never turn a
failure into a success. tether's own decoding and validation errors do not
pass through it. It is registered once, at start-up; a second call panics.

**Outside routes.** Middleware and plain handlers that answer a request
themselves, such as a session check, a CSRF check or panic recovery, use

```go
func WriteError(w http.ResponseWriter, r *http.Request, err error)
```

It applies `MapErrors` and the table above, so the client sees one error
format and gets the message, and a 500 is logged, not sent. It keeps the
headers already set on `w` and must be called before anything is written.
A nil error is a mistake in the caller and answers 500.

tether does not recover panics in `Route`: `net/http` already does, and one
recovery middleware around the mux that calls `WriteError` also covers the
other middleware. It must re-panic `http.ErrAbortHandler`, which aborts a
response on purpose.

### 3.6 Enums

A named type with underlying type `string` is an **enum** if its package
declares at least one constant of that type. Its values are those constants
in source order. Anything else is a plain string.

- A field of an enum type accepts only its values (`enum`), without a tag.
- An optional enum field also accepts `""` (not set).
- A constant with the value `""` is valid but left out of the TypeScript
  value list, because select inputs cannot offer an empty option.

The server needs the values at run time, and Go cannot list constants at
run time. The generator therefore writes `tether_gen.go` into every
**handler package** whose handlers take enums in their input:

```go
// Code generated by tether; DO NOT EDIT.

package handler

import (
	"github.com/maulai/tether"
	"example.com/shop/domain"
)

func init() {
	tether.RegisterEnum(domain.StatusOpen, domain.StatusBlocked, domain.StatusDone)
}
```

The handler package is the right place because `Route` refers to the
handler: Go links that package, and runs its `init`, wherever the route is
built, a test included, so the check can never be missing. The package that
declares an enum is never written to: a domain package does not depend on
tether, and an enum may live in another module of the workspace or in
generated code. Its constants are read from the type information, so its
package need not be among the package patterns; constants of an enum in
another package than the handler must be exported. Import names in the file
avoid each other and the names the package declares, and stay
deterministic.

Projects never call `RegisterEnum` themselves. When reading the source, the
generator ignores the `tether_gen.go` files it wrote, so a stale one never
blocks a run, and it removes its own files from packages that no longer
need one, including enum packages from earlier versions of tether. A file
of that name that tether did not write is left alone.

### 3.7 Response headers

A handler that needs to set a header, a caching rule, an `ETag` or a cookie,
gets the response headers from the request; the handler shapes stay the
same:

```go
func ResponseHeader(r *http.Request) http.Header
func SetCookie(r *http.Request, c *http.Cookie) error
```

- `ResponseHeader` panics for a request that `Route` did not serve: a header
  that silently goes nowhere is a bug.
- `SetCookie` adds a `Set-Cookie` header like `http.SetCookie`, but returns
  an error for an invalid cookie instead of dropping it silently. Returned
  from the handler, the error becomes a 500 with the cause in the log.
- **Headers apply to every answer**, an error included, as with `net/http`.
  Some belong exactly there: `Retry-After` with 429, `WWW-Authenticate` with
  401, a cookie cleared after a failed sign-in. A handler therefore sets a
  session cookie only once nothing can fail any more.
- tether decides `Content-Type`, `Content-Disposition` and `Content-Length`
  itself and overrides what a handler set; a 204 carries none of them.

Sessions themselves stay out of scope (9); these two functions are what a
project needs to build them.

### 3.8 Typed path parameters

```go
func PathValue[T any](r *http.Request, name string) (T, error)
```

parses a path parameter into `T`: a string type, an enum (only its values
are accepted), a number, a boolean, `time.Time`, or a type with
`UnmarshalText` such as `uuid.UUID`. A value that does not parse returns an
error that answers 404 with message `notFound`: a malformed address names
no resource. The handler returns it as it is. `PathValue` panics for a `T`
it cannot parse into and for a name the route's pattern does not have; both
are programming errors.

## 4. Validation

Rules come from the `v` struct tag: a comma separated list of rules, each
`name` or `name=argument`.

### 4.1 Built-in rules

| Rule | Applies to | Meaning | Message code |
|---|---|---|---|
| `required` | string | not empty and not only whitespace | `required` |
| | pointer, slice, map, `time.Time` | not nil, not empty, not zero | `required` |
| `requiredif=f:v` | any | required when sibling `f` equals `v` | `requiredif` |
| `forbiddenif=f:v` | any | must be empty when sibling `f` equals `v` | `forbiddenif` |
| `min=N`, `max=N` | string | length in characters | `minLength`, `maxLength` |
| | number | value, inclusive | `min`, `max` |
| | slice, map | number of entries | `minItems`, `maxItems` |
| `gt=N`, `lt=N` | number | value, exclusive | `gt`, `lt` |
| `email` | string | `^[^@\s]+@[^@\s]+\.[^@\s]+$` | `email` |
| `url` | string | `http://` or `https://`, a host, no whitespace | `url` |
| `oneof=a b c` | string | one of the listed values (space separated) | `oneof` |
| `dive` | slice, map | the following rules apply to each element | |

**Conditions.** In `requiredif` and `forbiddenif`, a leading `!` negates the
value (`forbiddenif=kind:!invoice` means "must be empty unless kind is
invoice"). An empty value means "is empty". The value is compared in the
sibling's type: `true` or `false` for a boolean, a number for a number,
text otherwise. A sibling that does not exist is an error when generating.

**Always on, without a tag:**
- `text`: every string, at every depth, is rejected if it contains control
  characters other than tab, line feed and carriage return, bidirectional
  override or isolate characters (U+202A to U+202E, U+2066 to U+2069), or
  the line and paragraph separators U+2028 and U+2029. Joiners used by
  emoji and many scripts stay allowed.
- `enum`: see 3.6.
- `integer`: a Go integer field accepts only whole numbers.
- Nested structs, also in slices and maps, are always validated.

**Empty optional values.** For a field without `required`, the empty string
means "not given": value rules (`min`, `email`, `url`, `oneof`, enum,
project rules) are skipped for `""`. Forms send `""` for an empty input, so
this keeps them valid. Values are never trimmed or otherwise changed.

**Missing numbers and booleans.** A number or boolean field without a
pointer that is missing from the input has its zero value, `0` or `false`,
as with `encoding/json`. The browser validator treats a missing value the
same way, so `min=1` rejects it on both sides with the same message.

**Mistakes in tags are errors when generating:** an unknown rule, a rule on
a type it does not apply to (`gt` on a string), or `required` on a plain
number or boolean (use a pointer; a missing number cannot be told apart from
zero).

### 4.2 Project rules

Domain formats are not built in. The project registers them, in an `init`
function with constant arguments, so the generator can read them:

```go
func init() {
	tether.Pattern("hexColor", `#[0-9a-fA-F]{6}`, "Use a colour like #1a2b3c.")
	tether.Check("ean13", validEAN13, "Not a valid EAN-13.")
}
```

| Function | Runs | Notes |
|---|---|---|
| `Pattern(name, expr, message)` | server and browser | the whole value must match. tether translates `expr` from Go's parse tree to JavaScript, writing out every class and case folding explicitly, and rejects what it cannot translate exactly (multiline anchors) |
| `Check(name, fn func(string) bool, message)` | server only | for checks a pattern cannot express, such as check digits |

Both apply to strings only. Tags use them by name: `v:"required,hexColor"`.
A name must not collide with a built-in rule. `message` is the default text
and must not be empty; `Messages` can override it like any other. The
generator copies a `Pattern`'s text into the TypeScript runtime (5); a
`Check`'s text stays on the server, the only side that reports it.

## 5. Messages

Every error carries a finished text. The text for a rule comes from a
message table; a handler writes its own text directly in `FieldError` or
`Fail`.

tether ships English texts. The project changes them with one call:

```go
import "github.com/maulai/tether/messages/de"

func init() {
	tether.Messages(de.Messages, map[string]string{
		"required": "Bitte ausfüllen.",
	})
}
```

- The arguments are merged from left to right over the built-in table.
- There is at most one `Messages` call per program; the generator reports a
  second one. Its arguments must be map literals or package level variables
  initialised with map literals, so the generator can read them.
- The generator copies the effective table into the TypeScript runtime, in
  the order the server looks a text up: the built-in texts, then each
  `Pattern`'s message, then `Messages`. The browser produces the same texts
  as the server.
- Placeholders in braces are replaced with the rule's argument: `{min}`,
  `{max}`, `{gt}`, `{lt}`, `{values}`.

| Code | Default text |
|---|---|
| `required`, `requiredif` | Required. |
| `forbiddenif` | Must be empty. |
| `type` | Invalid value. |
| `integer` | Must be a whole number. |
| `unknown` | Unknown field. |
| `text` | Contains invisible control characters. |
| `minLength` | Must be at least {min} characters. |
| `maxLength` | Must be at most {max} characters. |
| `min` | Must be at least {min}. |
| `max` | Must be at most {max}. |
| `gt` | Must be greater than {gt}. |
| `lt` | Must be less than {lt}. |
| `minItems` | Must contain at least {min} entries. |
| `maxItems` | Must contain at most {max} entries. |
| `enum` | Must be one of the offered values. |
| `oneof` | Must be one of: {values}. |
| `email` | Must be a valid email address. |
| `url` | Must be a web address starting with http:// or https://. |
| `invalid` | Please check the marked fields. |
| `malformed` | The request body could not be read. |
| `tooLarge` | The request is too large. |
| `internal` | Something went wrong. |
| `network` | The server cannot be reached. (browser only) |
| `range` | The requested range is not available. |
| `notFound` | Not found. |

## 6. Generator

```
go tool tether -out <dir> [-openapi <file>] [-title <text>] [-version <text>] [-check] [packages]
```

- `packages` defaults to `./...`, relative to the directory it runs in
  (for `go generate`, the directory of the file with the directive).
- `-out`: directory for the TypeScript files.
- `-openapi`: path of the OpenAPI document, default `openapi.json`.
- `-check`: write nothing; exit with status 1 and list every file that is
  missing or differs. For CI.

It finds every call to `tether.Route` in the loaded packages and follows
the types of every input and output.

### 6.1 Types

| Go | JSON Schema | TypeScript |
|---|---|---|
| `string` | `string` | `string` |
| enum (3.6) | `string` with `enum` | union of the values, plus a values array |
| `bool` | `boolean` | `boolean` |
| integer types | `integer` | `number` |
| float types | `number` | `number` |
| field with `json:",string"` | `string` | `string` |
| `time.Time` | `string`, `format: date-time` | `string` |
| `[]byte` | `string`, `format: byte` | `string` |
| `[]T`, `[N]T` | `array` | `T[]` |
| `map[K]V`, `K` string or named string | `object` | `Record<K, V>` |
| `*T` | `T` or `null` | `T \| null` |
| named struct | `$ref` to a component | `interface` |
| generic struct instance | component per instance, `Page_Task` | generic interface, `Page<Task>` |
| `json.RawMessage`, `any` | any value | `unknown` |
| type implementing `encoding.TextMarshaler` | `string` | `string` |

Struct fields follow `encoding/json`: the `json` name, `-` to skip,
embedded structs without a name are flattened, unexported fields are
skipped. A field with `omitempty` or `omitzero` is optional (`?`) in output
types. In input types, a field is optional unless it has `required`. A
struct reached from both an input and an output follows the input rule.

Errors when generating:
- a type implementing `json.Marshaler` but not `encoding.TextMarshaler`
  (its JSON shape is unknown; use a plain type in the API),
- channels, functions, complex numbers, maps with other key types,
- two types with the same name in different packages (rename one).

Doc comments on types and fields become descriptions in OpenAPI and in
TypeScript. An `example:"…"` tag becomes the example in OpenAPI.

### 6.2 OpenAPI

One OpenAPI 3.1 document for all routes:
- `operationId` is the TypeScript function name; `summary` is the first
  sentence of the handler's doc comment, `description` the whole comment.
- Path and query parameters, request body and responses with their schemas;
  every operation also lists the problem response for `400` and `422`.
- Rules become the matching JSON Schema keywords (`minLength`, `maximum`,
  `exclusiveMinimum`, `minItems`, `format`, `enum`, `pattern`). Conditions
  and server-only checks are written as sentences in `description`. JSON
  Schema cannot express everything exactly (it does not know "not only
  whitespace"); the document is a faithful description for other tools, the
  behaviour is defined here.
- Input objects say `additionalProperties: false`, because the server
  rejects unknown fields.

### 6.3 TypeScript

Two files in `-out`:

- `runtime.ts`: request handling, error classes, the validator, the message
  table. Identical in every project apart from the message table.
- `index.ts`: all types, enum value arrays, one validator per input type
  and one function per route.

**Route functions.** Path parameters come first, in pattern order, then the
input, then options:

```ts
/** UpdateTask changes a task. */
export function updateTask(id: string, input: TaskInput, options?: RequestOptions): Promise<Task>
```

- The input parameter is optional if the input type has no required field.
- Route functions send only the fields of the input type (`strip`), so a
  form may hold fields of its own.
- In the query string a field that is `undefined`, `null` or `""` is left
  out. A list sends every element, `null` and `undefined` as empty values,
  so the server sees each element at its position (3.3).
- A handler without output returns `Promise<void>`.
- `tether.File` output returns `Promise<Blob>`; a `GET` route with file
  output also gets `<name>Url(...)`, returning the address for links.
- `tether.Upload` input takes `file: Blob`; `options.onProgress` reports
  upload progress from 0 to 1.

**Configuration**, once at start-up:

```ts
configure({
  baseUrl: "",                                      // default: same origin
  headers: () => ({ "X-CSRF-Token": readCsrf() }),  // added to every request
});
```

**Errors.** A failed request throws `ApiError` with `status`, `message`
(the problem's `detail`), `errors` (field errors) and `headers` (the
response's headers, for a request id or `Retry-After`; empty for a network
failure). A `422` throws `ValidationError`, a subclass. A network failure
throws `ApiError` with status `0`. Aborting through `options.signal` is
never an `ApiError`: the request rejects with the signal's reason, as
`fetch` does (an `AbortError`, or a `TimeoutError` for
`AbortSignal.timeout`), an upload with progress too, and a signal that is
already aborted sends nothing. Across origins, the browser exposes only
the headers the server lists in `Access-Control-Expose-Headers`.

```ts
try {
  await createUser(values);
} catch (e) {
  if (e instanceof ValidationError) {
    for (const f of e.errors) form.setError(f.field, { message: f.message });
  }
}
```

**Validators.** One per input type, named `<type>Schema`
(`taskInputSchema`). Each implements Standard Schema v1, so form libraries
accept it directly (react-hook-form: `standardSchemaResolver`). It also
offers:
- `validate(value): FieldError[]`, the same field errors the server sends,
  in the same order,
- `pick(...keys)` and `omit(...keys)` for forms that show only some fields;
  conditions that refer to a removed field are dropped with it,
- `strip(value)`, the value with only the fields of the type.

The validator checks shape first (`type`, `integer`) and rules only when the
shape holds, like the server. Unlike the server it ignores unknown fields,
since route functions never send them. Project checks registered with
`Check` pass in the browser; the server reports them.

The generated code compiles under the strictest TypeScript settings
(`strict`, `exactOptionalPropertyTypes`, `noUncheckedIndexedAccess`,
`erasableSyntaxOnly`), and Node.js can run it directly.

Validators do not change values. Dates are ISO strings in both directions.

## 7. Tests

- **Conformance:** test vectors in `testdata/conformance` (fields, input,
  expected errors, and where the browser differs on purpose, its own
  expectation). The Go validator and the TypeScript runtime run the same
  vectors; the TypeScript side runs with Node.js and adds no dependency to
  the repository.
- **Golden files:** the generator runs over `internal/gen/testdata/features`
  and the example and compares OpenAPI, TypeScript and `tether_gen.go` byte
  for byte; `internal/gen/testdata/mistakes` pins the error report.
  `go test ./internal/gen -update` rewrites them.
- **End to end:** the example's test serves its API and drives it with the
  generated client in Node.js, asserting that browser and server report the
  same errors for the same input.
- **Strict TypeScript:** `tsc` type-checks the generated code in CI.

## 8. Versions

tether uses semantic versioning, `v0.x` until the API is stable. CI
releases every push to `main` that passes the tests: it tags the next
version and creates a GitHub release with generated notes. A commit chooses
the step with a trailer line `Release: minor`, `Release: major`, or
`Release: none` for changes that need no release (documentation, CI);
without one the step is a patch. Projects pin a version as a tool
dependency:

```
go get -tool github.com/maulai/tether/cmd/tether@v0.1.0
```

This also pins the library, because both live in one module, and builds the
command with the project's own Go version. Generated files name the version
that wrote them when it is a release tag; a development build writes no
version, so its output does not change with every commit.

## 9. Out of scope

Routing beyond `Route`, middleware, authentication, sessions, CSRF tokens
(set them through `configure`), caching and query keys, form components,
documentation pages, request bodies other than JSON and single file
uploads, streaming, WebSockets, integer enums.
