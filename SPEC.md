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

// UpdateTask replaces a task.
func UpdateTask(r *http.Request, id int, in TaskInput) (Task, error) { … }

mux.Handle(tether.Route("POST /v1/tasks", CreateTask))
mux.Handle(tether.Route("PUT /v1/tasks/{id}", UpdateTask))
```

```ts
import { createTask, taskInputSchema } from "./api";

const task = await createTask({ title: "Write spec", status: "open" });
await updateTask(task.id, { ...task, status: "done" }); // id: number
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
func Route(pattern string, handler any, opts ...Options) (string, http.Handler)
```

`pattern` is a `net/http.ServeMux` pattern with a method, for example
`"PUT /v1/tasks/{id}"`. It must be a string constant, because the generator
reads it from the source. Supported methods: `GET`, `POST`, `PUT`, `PATCH`,
`DELETE`. `opts`, at most one, says who may call the route (3.9).

The return values fit `mux.Handle` directly. Middleware wraps the mux or a
handler as usual:

```go
mux.Handle(tether.Route("GET /v1/tasks", ListTasks))
http.ListenAndServe(":8080", requestID(mux))
```

Other routers work if they set path values through `Request.SetPathValue`.

### 3.2 Handlers

A handler is a **named function or method**. It takes `r`, then one
argument per parameter of the pattern, in the pattern's order, then
optionally its input, and returns an output and an error, or only an
error:

```go
func(r *http.Request, <path parameters…>[, in In]) ([Out, ]error)
```

```go
func ListTasks(r *http.Request) ([]Task, error)                      // GET /v1/tasks
func CreateTask(r *http.Request, in TaskInput) (Task, error)         // POST /v1/tasks
func GetTask(r *http.Request, id int) (Task, error)                  // GET /v1/tasks/{id}
func UpdateTask(r *http.Request, id int, in TaskInput) (Task, error) // PUT /v1/tasks/{id}
func DeleteTask(r *http.Request, org string, id int) error           // DELETE /v1/orgs/{org}/tasks/{id}
```

`Route` panics at startup for any other shape. `In` must be a named,
non-generic struct type (or `tether.Upload`). A pattern with parameters
always has them in the handler; one the handler does not need is named
`_`.

- **Name:** the TypeScript function is named after the handler, with its
  leading capitals lowercased (`CreateTask` becomes `createTask`, `HTTPGet`
  becomes `httpGet`). Two handlers with the same name, or a name that is a
  reserved word in TypeScript (`Delete`), are an error that names the place.
- **Description:** the handler's doc comment becomes the operation's
  description in OpenAPI and the doc comment of the TypeScript function.
- **Path parameters:** see 3.8. In TypeScript they are arguments of
  their types, before the input.

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
- `PUT` replaces the whole record, so its body must send every field, in
  nested structs and in structs inside lists and maps too. A field it
  lacks is a field error (`missing`) at its path, `links` or
  `items.2.note`, instead of a value cleared without notice. `null` counts
  as sent. A field with `omitempty` or `omitzero` may be left out, for a
  value the handler keeps when it is not sent; the entries of a map are
  data, not fields, and are not required. `POST` and `PATCH` take what they
  get.
- For a number, a `time.Time` or a type with a Format (4.2), `""` means
  "not given": forms send it for an empty input or select.
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

Besides the members of RFC 9457, a problem may carry `code`, the case of a
`Problem` (below), and a 500 carries `instance`, a `urn:uuid:` that the
server's log names next to the cause.

`field` is the path in the JSON input, with dots and numeric indices:
`items.2.sku`, `labels.de`. This is the notation form libraries such as
react-hook-form use, so it can be passed to them directly.

Handlers create errors with:

```go
func FieldError(field, message string) error // 422, one field
func Fail(status int, message string) error  // any status, no field

type Problem struct {
	Status  int
	Code    string
	Message string
}
```

Several field errors are combined with the standard `errors.Join`.

**Problems.** A `Problem` with a `Code` is a case the client tells apart
from others with the same status, to react to it: a session that waits for
its second factor, an archive that is suspended. It is declared once, as a
package variable, and returned as it is:

```go
// ErrMFARequired: the session waits for the second factor.
var ErrMFARequired = tether.Problem{Status: http.StatusForbidden, Code: "mfaRequired", Message: "Two-factor sign-in required."}

return ErrMFARequired
return ErrSuspended.With("This archive is suspended: " + reason) // another text, the same case
```

- `Code` is lowerCamel, `Status` a constant from 400 to 599; the generator
  reads every `Problem` literal with a code, reports a code declared twice
  and gives the client a function per code (6.3).
- `errors.Is(err, ErrMFARequired)` holds for the problem and for every
  `With` of it, so Go code and tests recognise the case too.
- `Fail(status, message)` is a `Problem` without a code: for a text the
  client shows rather than reacts to, such as most conflicts.

| Error | Status | `detail` |
|---|---|---|
| validation or `FieldError` | 422 | message `invalid` |
| body is not valid JSON | 400 | message `malformed` |
| body too large | 413 | message `tooLarge` |
| `Problem` or `Fail(status, message)`, status 400 to 599 | `status`, and `code` | `message` |
| a panic in the handler or in a check (3.9) | 500 | message `internal`; the panic and its stack are logged |
| any other error, a `Problem` with another status included | 500 | message `internal`; the error is logged with `log/slog` and not sent |

A project needs no error type of its own: `FieldError` and `Problem` cover
every status (use the `net/http` constants), and any other error is answered
with 500 and logged, which keeps technical causes out of responses. Errors
of code that must not import tether go through `MapErrors`.

**Instance.** Every 500 gets a new `instance`, `urn:uuid:` and a random
UUID. The log line with the cause names the same instance, so a person who
reports the error page names the entry that explains it.

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

**Panics.** `Route` recovers a panic in the handler or in its check and
answers 500 with an instance, the panic and its stack in the log: without
this, `net/http` breaks the connection off, and the client reports an
unreachable server instead of a failed request. tether writes every byte
of the response itself, so it knows when it is too late: a panic once the
response has begun, while a file streams, is logged and aborts the
response. `http.ErrAbortHandler`, which aborts a response on purpose,
passes on. Middleware around the mux recovers its own panics if it needs
to.

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
**handler package** whose handlers take enums in their input or their path
parameters:

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

### 3.8 Path parameters

A handler takes the parameters of its pattern as arguments after `r`, in
the pattern's order, each parsed into its type before the handler runs:

```go
// GetTask returns one task.
func GetTask(r *http.Request, id uuid.UUID) (Task, error) { … }

mux.Handle(tether.Route("GET /v1/tasks/{id}", GetTask))
```

- A parameter is a string type, an enum (only its values are accepted), a
  number, a boolean, `time.Time`, or a type with `UnmarshalText` such as
  `uuid.UUID`, which needs a Format (4.2).
- A value that does not parse answers 404 with message `notFound`: a
  malformed address names no resource. The handler never sees it.
- `{name...}`, the rest of the path, is a string; `{$}` is no parameter.
- The generator checks the names: the argument for `{id}` is named `id`,
  or `_`. `Route` checks the number and the types, and panics at start-up
  for a mismatch.
- A query parameter must not have the name of a path parameter of its
  route: both would sit in the address. The generator reports it.
- Middleware reads a path value with `r.PathValue` as usual.

### 3.9 Access

Who may call a route is part of the API: it belongs in the OpenAPI
document, and declaring it apart from its check would let the two drift.
A project declares each way a caller proves who it is once, with its
check, and each route says which one it needs:

```go
// Session is the sign-in of the browser: a session cookie and a CSRF header.
var Session = tether.Auth{Cookie: "session_token", Check: checkSession}

// APIKey is the key of another system.
var APIKey = tether.Auth{Bearer: true, Check: checkKey}

var (
	open     = tether.Options{Public: true}
	signedIn = tether.Options{Auth: Session}
	admin    = tether.Options{Auth: Session, Scopes: []string{"admin"}}
	ingest   = tether.Options{Auth: APIKey, Scopes: []string{"documents:write"}}
	reader   = tether.Options{Auths: []tether.Auth{Session, APIKey}, Scopes: []string{"catalog:read"}}
)

mux.Handle(tether.Route("POST /v1/login", Login, open))
mux.Handle(tether.Route("GET /v1/me", GetMe, signedIn))
mux.Handle(tether.Route("POST /v1/categories", CreateCategory, admin))
mux.Handle(tether.Route("POST /v1/ingest/documents", CreateIngestDocuments, ingest))
mux.Handle(tether.Route("GET /v1/products", ListProducts, reader))
```

```go
type Auth struct {
	Cookie string // the credential is the cookie of this name
	Header string // … the header of this name
	Bearer bool   // … Authorization: Bearer
	Check  func(r *http.Request, scopes []string) (*http.Request, error)
}

type Options struct {
	Auth   Auth   // the one way in
	Auths  []Auth // or several, for browsers and other systems alike
	Scopes []string
	Public bool
}
```

- **The check is the project's.** tether knows no session and no key; it
  calls `Check` for every request to the route, with the route's scopes,
  before the path parameters and the input are read, so a caller without
  credentials learns nothing about them. `Check` returns the request with
  what the handler needs in its context, derived from `r` with
  `r.WithContext` (nil means `r`), or an error, answered like a handler's
  error: a `Problem` with a code lets the client tell "signed out" from
  "second factor missing". Response headers and cookies can be set in it
  (3.7).
- **One declaration, two readers.** The generator reads the same values
  for OpenAPI: the scheme is named like the variable and described by its
  doc comment, every route lists its security and the answers 401 and 403.
  A route cannot be documented as protected and served open.
- **Every route says it.** Once one route of a program has an `Auth`,
  every route has `Auth`, `Auths` or `Public`; a route without one is an
  error when generating. The route list is then the access table of the
  API.
- **Several ways in.** A route that browsers and other systems call alike
  lists its alternatives in `Auths`, in order. tether calls the check of
  the first whose credential the request carries: the cookie of that name
  with a value, the header of that name with a value, or `Authorization`
  with the scheme `Bearer` (any case) and a token. A request that carries
  none hears from the first check, so the project answers with its own
  problem. A check that fails is the answer: tether does not try the next,
  so a stale cookie never falls through to a key, and the way in that was
  checked is always the one the request chose. tether only looks where a
  credential is; whether it is good is the check's.
- The scopes are the route's, the same for every alternative; each check
  enforces them in its own terms, roles of a session or scopes of a key,
  since OpenAPI lists them with each. A cross-site request forged in a
  browser carries only the cookie, never a header it would need a
  preflight for, so it always reaches the session's check, whatever the
  order.
- An `Auth` names exactly one of `Cookie`, `Header` and `Bearer`, and has a
  `Check`. `Public`, `Auth` and `Auths` exclude each other; `Auths` has two
  or more, each read from its own place (a bearer token travels in the
  `Authorization` header); `Scopes` need an `Auth`. `Route` panics for
  these mistakes, the generator reports them.
- `Options` are written in the call or as package variables initialised
  with a literal, `Auths` as a literal of package variables, and an `Auth`
  is a package variable, so the generator can read them.
- Middleware that answers by itself, a rate limit or a CSRF check, still
  answers with `WriteError`; what depends on the caller's identity, such as
  choosing a tenant's database, belongs in the check.

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
| `Format[T](name, expr, message)` | server and browser | a `Pattern` bound to `T`, a type with `UnmarshalText` (below) |

`Pattern` and `Check` apply to strings only. Tags use them by name: `v:"required,hexColor"`.
A name must not collide with a built-in rule. `message` is the default text
and must not be empty; `Messages` can override it like any other. The
generator copies a `Pattern`'s text into the TypeScript runtime (5); a
`Check`'s text stays on the server, the only side that reports it.

**Formats.** A type that reads itself with `UnmarshalText`, such as
`uuid.UUID`, travels as text, and only the server can run its
`UnmarshalText`. A Format gives the browser the same check:

```go
func init() {
	tether.Format[uuid.UUID]("uuid", `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, "Not a valid ID.")
}
```

- Every value of `T`, in a field at any depth, in the query string and in a
  path, must match the pattern before `UnmarshalText` sees it, on the
  server and in the browser; a value that does not match is reported with
  rule `name` and the message. Both check it with the shape, before the
  rules (6.3), so it comes with `type` errors, not after them.
- `""` means "not given" (3.3): an optional field stays nil or zero, a
  required one is `required`.
- A value the pattern lets through and `UnmarshalText` refuses is `type` on
  the server only. The pattern is the project's promise that this does not
  happen; write it at least as strict as the type.
- `name` is a rule like a `Pattern`, so tags can use it on plain strings,
  and `Messages` can replace its text. In OpenAPI it is the `format` of the
  type, with the pattern.
- The generator reports a route input or a path parameter of a type with
  `UnmarshalText` that has no Format: the browser would let through what the
  server rejects. `Validate` reads no text and needs none.
- `Format` panics for a `T` without `UnmarshalText`, for `time.Time`, and
  for a type registered twice.

### 4.3 Outside routes

```go
func Validate(v any) error     // v is a struct or a pointer to one
func Issues(err error) []Issue // Issue{Field, Rule, Message}
```

`Validate` checks a value with the rules of its tags and its enums, exactly
as a route checks its input after reading it: for input that does not
arrive over HTTP, and for tests of the rules without a route. It returns
nil, or the field errors joined into one error, which a handler can return
as it is to answer 422. `missing` does not apply, since a Go value has no
missing fields.

`Issues` lists the field errors in an error as a 422 response would: those
of `Validate`, of `FieldError` and of `errors.Join` over them.

The generator registers the enums of every type passed to `Validate`, in
the package that calls it, so the argument must have a named struct type
there; `Validate(v)` with `v any` is reported. Like `Route`, `Validate`
panics for a value that is not a struct and for a mistake in a tag.

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
| `missing` | Missing. A PUT sends every field; null or an empty value clears it. |
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
go tool tether -out <dir> [-openapi <file>] [-title <text>] [-version <text>] [-qualify] [-check] [packages]
```

- `packages` defaults to `./...`, relative to the directory it runs in
  (for `go generate`, the directory of the file with the directive).
- `-out`: directory for the TypeScript files.
- `-openapi`: path of the OpenAPI document, default `openapi.json`.
- `-qualify`: start every type name in TypeScript and OpenAPI with its
  package name (6.1), for programs that use one name in several packages.
- `-check`: write nothing; exit with status 1 and list every file that is
  missing or differs. For CI.

It finds every call to `tether.Route` in the loaded packages, with its
Options (3.9), and follows the types of every path parameter, input and
output. It also reads every `tether.Validate` call (4.3), to check the type
and register its enums, every `Pattern`, `Check` and `Format` call, and every
`Problem` literal with a code (3.5).

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
| … with a Format (4.2) | `string`, `format` and `pattern` | `string` |

Struct fields follow `encoding/json`: the `json` name, `-` to skip,
embedded structs without a name are flattened, unexported fields are
skipped. A field with `omitempty` or `omitzero` is optional (`?`) in output
types. In input types, a field is optional unless it has `required`. A
struct reached from both an input and an output follows the input rule.
A struct reached from the input of a `PUT` route must be sent whole (3.3),
so all its fields are required, as in output types; this holds for every
route that uses it, a `POST` too, and the compiler then finds a forgotten
field before the server does.

**Names.** A type keeps its Go name: `Task`, `Page<Task>`. With `-qualify`
it starts with its package name in UpperCamel, everywhere: `tax.Class` is
`TaxClass` with `taxClassValues`, `tax.VatRateInput` has
`taxVatRateInputSchema`, `httpx.Page[tax.VatRate]` is
`HttpxPage<TaxVatRate>` and the component `HttpxPage_TaxVatRate`. A name
that already starts with the package name as a word keeps it once:
`media.MediaFile` is `MediaFile`, `document.Document` is `Document`, while
`tax.Taxonomy` becomes `TaxTaxonomy`. Route functions, field names, error
paths and `tether_gen.go` do not change.

Errors when generating:
- a type implementing `json.Marshaler` but not `encoding.TextMarshaler`
  (its JSON shape is unknown; use a plain type in the API),
- channels, functions, complex numbers, maps with other key types,
- two types with the same name in different packages (rename one, or use
  `-qualify`); with `-qualify`, two packages of the same name that both
  contribute such a type.

Doc comments on types and fields become descriptions in OpenAPI and in
TypeScript. An `example:"…"` tag becomes the example in OpenAPI.

### 6.2 OpenAPI

One OpenAPI 3.1 document for all routes:
- `operationId` is the TypeScript function name; `summary` is the first
  sentence of the handler's doc comment, `description` the whole comment.
- Path and query parameters, request body and responses with their schemas;
  a path parameter has the schema of its type. Every operation also lists
  the problem response for `400` and `422`.
- With an `Auth` (3.9): `securitySchemes` named like the variables, an
  `apiKey` in a cookie or header or `http` bearer, described by their doc
  comments; every operation has `security`, `[]` for a public one and one
  requirement per alternative of `Auths`, and a protected one lists `401`
  and `403`.
- The `Problem` schema lists every declared code with its status and text,
  and `instance`.
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
export function updateTask(id: number, input: TaskInput, options?: RequestOptions): Promise<Task>
```

- A path parameter has the type of its Go argument: `number`, an enum,
  `boolean` or `string`.
- With an `Auth` in the program, the doc comment says who may call the
  route: `Access: Session, with admin.`, `Access: Session or APIKey, with
  catalog:read.`, `Access: public.`

- The input parameter is optional if the input type has no required field.
  The input type of a `PUT` route has every field required (6.1), so the
  compiler reports a field the call forgets.
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
(the problem's `detail`), `errors` (field errors), `code` (the case of a
`Problem`, typed as the union `ProblemCode` of the declared codes),
`instance` (for a 500) and `headers` (the response's headers, for
`Retry-After`; empty for a network failure). A `422` throws
`ValidationError`, a subclass. A network failure
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

**Problems.** For every declared code `index.ts` has a function named
after it, which takes the caught value as it is:

```ts
/** ErrMFARequired: the session waits for the second factor. Status 403, code mfaRequired. */
export function isMfaRequired(e: unknown): e is ApiError & { readonly code: "mfaRequired" }

if (isMfaRequired(e)) navigate("/auth/mfa");
```

**Validators.** One per input type, named `<type>Schema`
(`taskInputSchema`). Each implements Standard Schema v1, so form libraries
accept it directly (react-hook-form: `standardSchemaResolver`). It also
offers:
- `validate(value): FieldError[]`, the same field errors the server sends,
  in the same order; a Format is checked with the shape, as on the
  server,
- `pick(...keys)` and `omit(...keys)` for forms that show only some fields;
  conditions that refer to a removed field are dropped with it,
- `strip(value)`, the value with only the fields of the type.

The validator checks shape first (`type`, `integer`) and rules only when the
shape holds, like the server. Unlike the server it ignores unknown fields,
since route functions never send them. Project checks registered with
`Check` pass in the browser; the server reports them. The same holds for
`missing`, which the types already prevent.

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
- **Golden files:** the generator runs over `internal/gen/testdata/features`,
  over `internal/gen/testdata/qualify` with `-qualify`, and over the example,
  and compares OpenAPI, TypeScript and `tether_gen.go` byte for byte;
  `internal/gen/testdata/mistakes` and `access` pin the error reports.
  `go test ./internal/gen -update` rewrites them.
- **End to end:** the example's test serves its API and drives it with the
  generated client in Node.js, asserting that browser and server report the
  same errors for the same input.
- **Strict TypeScript:** `tsc` type-checks the generated code in CI.

## 8. Versions

tether uses semantic versioning, `v0.x` until the API is stable. CI
releases every push to `main` that passes the tests: it tags the next
version and creates a GitHub release. A commit chooses the step with a
trailer line `Release: minor`, `Release: major`, or `Release: none` for
changes that need no release (documentation, CI); without one the step is
a patch.

The release notes come from the commits as well. Trailer lines at the end
of a message, in the block with `Release:`, say what a user of tether must
know: `Breaking:` for a change that needs work in a project, `Added:` for
something new, `Fixed:` for a bug that is gone. The notes list them by
kind, breaking changes first; a release without any lists its commit
subjects. A `Breaking:` trailer needs a step that may break, minor while
the major version is 0; ci fails otherwise, before the push is released.

```
Breaking: A handler takes its path parameters as arguments; PathValue is gone.
Added: tether.Format checks a text type such as uuid.UUID in the browser.
Release: minor
```

Projects pin a version as a tool dependency:

```
go get -tool github.com/maulai/tether/cmd/tether@v0.1.0
```

This also pins the library, because both live in one module, and builds the
command with the project's own Go version. Generated files name the version
that wrote them when it is a release tag; a development build writes no
version, so its output does not change with every commit.

## 9. Out of scope

Routing beyond `Route`, middleware, how a caller is authenticated (sessions,
keys and CSRF tokens are the project's `Check`; set request headers through
`configure`), caching and query keys, form components,
documentation pages, request bodies other than JSON and single file
uploads, streaming, WebSockets, integer enums.
