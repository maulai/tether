# tether

Write your HTTP API once, in Go. tether serves it with the standard library,
validates every request with rules you put in struct tags, and generates an
OpenAPI document and a fully typed TypeScript client that validates with the
same rules and the same messages.

- **Go is the only source.** Types, routes and validation rules live in Go;
  the TypeScript is generated and never edited.
- **Same rules, same result, same text** in the browser and on the server,
  checked by a shared test suite.
- **No framework.** The library uses only the standard library and works
  with `net/http`'s `ServeMux`. The generated TypeScript has no dependencies.

## Install

Go 1.25 or newer:

```sh
go get github.com/maulai/tether
go get -tool github.com/maulai/tether/cmd/tether
```

The second line pins the generator in your `go.mod`, so `go tool tether`
runs the same version as the library, built with your Go.

## Server

```go
//go:generate go tool tether -out web/src/api

type Status string

const (
	StatusOpen Status = "open"
	StatusDone Status = "done"
)

type TaskInput struct {
	Title  string `json:"title"  v:"required,max=120"`
	Status Status `json:"status" v:"required"`
}

// CreateTask adds a task.
func CreateTask(r *http.Request, in TaskInput) (Task, error) {
	if exists(in.Title) {
		return Task{}, tether.FieldError("title", "A task with this title exists already.")
	}
	return save(in), nil
}

// GetTask returns one task.
func GetTask(r *http.Request, id int) (Task, error) {
	return load(id)
}

func main() {
	mux := http.NewServeMux()
	mux.Handle(tether.Route("POST /v1/tasks", CreateTask))
	mux.Handle(tether.Route("GET /v1/tasks/{id}", GetTask))
	http.ListenAndServe(":8080", mux)
}
```

A handler is a named function. It takes `r`, then the parameters of its
pattern in their order, then optionally its input, and returns an output
and an error, or only an error (`204 No Content`):

```go
func ListTasks(r *http.Request) ([]Task, error)                      // GET /v1/tasks
func CreateTask(r *http.Request, in TaskInput) (Task, error)         // POST /v1/tasks
func GetTask(r *http.Request, id int) (Task, error)                  // GET /v1/tasks/{id}
func UpdateTask(r *http.Request, id int, in TaskInput) (Task, error) // PUT /v1/tasks/{id}
func DeleteTask(r *http.Request, id int) error                       // DELETE /v1/tasks/{id}
```

A path parameter is parsed into its type before the handler runs: strings,
numbers, booleans, enums, `time.Time` and types such as `uuid.UUID` (see
Formats). A value that does not parse answers 404, and the client takes it
in its type, `getTask(id: number)`. A parameter the handler does not need
is named `_`.

`In` comes from the query string for `GET` and `DELETE`, and from the JSON
body otherwise. tether decodes and validates `In` before your handler runs;
invalid input never reaches it. `Out` is written as JSON (`201` for `POST`,
`200` else).

A `PUT` replaces the whole record, so its body must send every field;
`null` or an empty value clears one. A forgotten field is an error
(`missing`) instead of a value cleared without notice, and the generated
TypeScript type asks for all of them.

Errors are RFC 9457 problem details:

| Return | Status |
|---|---|
| `tether.FieldError("title", "…")` (combine several with `errors.Join`) | 422, the message at the field |
| `tether.Fail(http.StatusConflict, "…")` | that status, with that message |
| a `tether.Problem` (below) | its status, its message and its code |
| any other error, or a panic | 500 with a generic message and an `instance`; the cause goes to `log/slog` with the same instance |

A case the client must tell apart from others with the same status is a
`Problem` with a code, declared once:

```go
// ErrMFARequired: the session waits for the second factor.
var ErrMFARequired = tether.Problem{Status: http.StatusForbidden, Code: "mfaRequired", Message: "Two-factor sign-in required."}

return ErrMFARequired
return ErrSuspended.With("This archive is suspended: " + reason) // another text, the same code
```

The client gets a function for it, `isMfaRequired(e)`, and `errors.Is`
recognises it in Go.

Errors of packages you cannot change are translated in one place:

```go
func init() {
	tether.MapErrors(func(err error) error {
		if errors.Is(err, pgx.ErrNoRows) {
			return tether.Fail(http.StatusNotFound, "Not found.")
		}
		return err
	})
}
```

Middleware answers in the same format with `tether.WriteError`, so the
client gets the message:

```go
func rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow(r) {
			tether.WriteError(w, r, tether.Fail(http.StatusTooManyRequests, "Too many requests."))
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

A panic in a handler is answered like any other unexpected error, so the
client sees a failed request, not an unreachable server.

Files are returned as `tether.File`. A body that can seek, such as an
`*os.File` or an S3 object, answers `Range` requests (a PDF viewer loading
page by page, a resumed download) and `If-Modified-Since`:

```go
// Document serves a stored document.
func Document(r *http.Request, id string) (tether.File, error) {
	obj, info, err := store.Open(r.Context(), id)
	if err != nil {
		return tether.File{}, err
	}
	return tether.File{Name: info.Name, ContentType: info.Type, Body: obj, ModTime: info.Modified}, nil
}
```

Set `Attachment: true` to make the browser download instead of show it.

Headers and cookies are set through the request, and apply to error
responses too:

```go
// SignIn starts a session.
func SignIn(r *http.Request, in Credentials) error {
	id, err := sessions.Start(r.Context(), in)
	if err != nil {
		return err
	}
	return tether.SetCookie(r, &http.Cookie{Name: "session", Value: id, HttpOnly: true, Secure: true, Path: "/"})
}
```

For other headers: `tether.ResponseHeader(r).Set("Cache-Control", "no-store")`.

## Access

Who may call a route is part of the API. Each way a caller proves who it
is is declared once, with the project's check, and each route says which
it needs:

```go
// Session is the sign-in of the browser: a session cookie and a CSRF header.
var Session = tether.Auth{Cookie: "session_token", Check: checkSession}

var (
	open     = tether.Options{Public: true}
	signedIn = tether.Options{Auth: Session}
	admin    = tether.Options{Auth: Session, Scopes: []string{"admin"}}
)

mux.Handle(tether.Route("POST /v1/login", Login, open))
mux.Handle(tether.Route("GET /v1/me", GetMe, signedIn))
mux.Handle(tether.Route("POST /v1/categories", CreateCategory, admin))
```

The check is the project's code; it returns the request with what the
handler needs in its context:

```go
func checkSession(r *http.Request, scopes []string) (*http.Request, error) {
	account, err := sessions.Account(r)
	if err != nil {
		return nil, err // a Problem, such as ErrMFARequired
	}
	if slices.Contains(scopes, "admin") && !account.IsAdmin {
		return nil, ErrAdminOnly
	}
	return r.WithContext(withAccount(r.Context(), account)), nil
}
```

tether calls the check before it reads the input, and the OpenAPI document
lists the schemes and every route's security, so documentation and check
cannot drift. Once one route has an `Auth`, every route says `Auth` or
`Public`; a forgotten one is an error when generating.

## Generate

```sh
go generate ./...
```

This writes `openapi.json`, `web/src/api/index.ts` and `web/src/api/runtime.ts`,
and a `tether_gen.go` that registers enums into the packages whose handlers
use them. Commit them. In CI, fail when they are out of date:

```sh
go tool tether -out web/src/api -check
```

If two packages use the same type name, add `-qualify`: every type name
then starts with its package, `document.Status` becomes `DocumentStatus`.

## Frontend

Every handler becomes a typed function with the same name:

```ts
import { configure } from "./api/runtime";
import { createTask, getTask, taskInputSchema } from "./api";

configure({ headers: () => ({ "X-CSRF-Token": readCsrfCookie() }) }); // optional

const task = await createTask({ title: "Write the README", status: "open" }); // Task
const same = await getTask(task.id);
```

Each input type has a validator that runs the server's rules in the browser.
It implements [Standard Schema](https://standardschema.dev), so form
libraries take it directly. With react-hook-form:

```ts
import { standardSchemaResolver } from "@hookform/resolvers/standard-schema";
import type { Path } from "react-hook-form";
import { ValidationError } from "./api/runtime";

const form = useForm({ resolver: standardSchemaResolver(taskInputSchema) });

async function onSubmit(values: TaskInput) {
  try {
    await createTask(values);
  } catch (e) {
    // Errors only the server can find (a taken title) arrive the same way.
    if (e instanceof ValidationError) {
      for (const f of e.errors) form.setError(f.field as Path<TaskInput>, { message: f.message });
    } else throw e;
  }
}
```

For a form that edits only some fields: `taskInputSchema.pick("title")`.

A failed request throws `ApiError` (or `ValidationError` for a 422) with
the problem's `code`, the `instance` of a 500 for an error page, and the
response's `headers`, for `Retry-After`. Across origins, list such headers
in `Access-Control-Expose-Headers`. A declared problem has its own test:

```ts
import { isMfaRequired } from "./api";

try {
  await getMe();
} catch (e) {
  if (isMfaRequired(e)) navigate("/auth/mfa");
}
```

## Rules

Rules go in the `v` tag, separated by commas.

| Rule | Meaning |
|---|---|
| `required` | not empty; text that is only whitespace counts as empty |
| `requiredif=field:value`, `forbiddenif=field:value` | required or forbidden when a sibling has a value; `!value` negates, an empty value means "is empty" |
| `min=N`, `max=N` | length of text, entries of a list, or value of a number |
| `gt=N`, `lt=N` | exclusive bounds for numbers |
| `email`, `url` | an email address; an `http` or `https` address |
| `oneof=a b c` | one of the listed values |
| `dive` | the rules after it apply to each element of a list or map |

Always checked, without a tag: a field of an enum type takes only its values;
text must not contain control characters, direction overrides or line
separators; nested structs are validated too. An empty optional text skips
its rules, and a missing number is `0`, as in Go. For a number that must be
sent, use a pointer.

The same checks run without a route, for input that does not come over
HTTP and in tests: `tether.Validate(v)` returns the field errors, and
`tether.Issues(err)` lists them.

**Enums** are plain Go: a string type and its constants, in any package or
module. tether finds them; the generated `tether_gen.go` in the package of
the handler registers them for the server, so domain packages need no
dependency on tether. Constants of an enum in another package must be
exported.

## Your own rules

tether has no built-in domain formats. Register yours once:

```go
func init() {
	tether.Pattern("hexColor", `#[0-9a-fA-F]{6}`, "Use a colour like #1a2b3c.")
	tether.Pattern("currency", `[A-Z]{3}`, "Use a three letter code like EUR.")
	tether.Pattern("slug", `[a-z0-9]+(?:-[a-z0-9]+)*`, "Use lower case letters, digits and dashes.")
	tether.Check("ean13", validEAN13, "Not a valid EAN-13.") // Go only; the server reports it
}
```

Then use them by name: `v:"required,hexColor"`. A `Pattern` must match the
whole value and runs in the browser too; tether translates it to JavaScript
exactly or refuses it.

## Formats

A type that reads itself with `UnmarshalText`, such as `uuid.UUID`, needs a
Format, so the browser checks it as the server does:

```go
func init() {
	tether.Format[uuid.UUID]("uuid", `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, "Not a valid ID.")
}
```

Every value of the type, in the body, the query string and the path, must
match before `UnmarshalText` sees it, with the Format's message on both
sides; `""` means "not given". In OpenAPI it is `format: uuid` with the
pattern. Without a Format, such a type in an input is an error when
generating.

## Messages

Every error carries a finished text. The built-in texts are English; German
ships with tether, and any text can be replaced:

```go
import "github.com/maulai/tether/messages/de"

func init() {
	tether.Messages(de.Messages, map[string]string{
		"required":  "Bitte ausfüllen.",
		"maxLength": "Höchstens {max} Zeichen.",
	})
}
```

The generator copies the table into the TypeScript runtime, so the browser
shows exactly what the server would send.

## More

- [`example/tasks`](example/tasks): a complete API with its generated client
  and an end-to-end test.
- [`SPEC.md`](SPEC.md): the full specification of behaviour.

## License

MIT
