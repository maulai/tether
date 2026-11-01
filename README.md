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

func main() {
	mux := http.NewServeMux()
	mux.Handle(tether.Route("POST /v1/tasks", CreateTask))
	mux.Handle(tether.Route("GET /v1/tasks/{id}", GetTask))
	http.ListenAndServe(":8080", mux)
}
```

A handler is a named function with one of these shapes:

```go
func(r *http.Request) (Out, error)
func(r *http.Request, in In) (Out, error)
func(r *http.Request) error             // 204 No Content
func(r *http.Request, in In) error
```

`In` comes from the query string for `GET` and `DELETE`, and from the JSON
body otherwise. Path parameters are read as usual: `r.PathValue("id")`.
tether decodes and validates `In` before your handler runs; invalid input
never reaches it. `Out` is written as JSON (`201` for `POST`, `200` else).

Errors are RFC 9457 problem details:

| Return | Status |
|---|---|
| `tether.FieldError("title", "…")` (combine several with `errors.Join`) | 422, the message at the field |
| `tether.Fail(http.StatusConflict, "…")` | that status, with that message |
| any other error | 500 with a generic message; the error goes to `log/slog` |

Files are returned as `tether.File`. A body that can seek, such as an
`*os.File` or an S3 object, answers `Range` requests (a PDF viewer loading
page by page, a resumed download) and `If-Modified-Since`:

```go
// Document serves a stored document.
func Document(r *http.Request) (tether.File, error) {
	obj, info, err := store.Open(r.Context(), r.PathValue("id"))
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

## Generate

```sh
go generate ./...
```

This writes `openapi.json`, `web/src/api/index.ts` and `web/src/api/runtime.ts`,
and a `tether_gen.go` into packages that declare enums. Commit them. In CI,
fail when they are out of date:

```sh
go tool tether -out web/src/api -check
```

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

**Enums** are plain Go: a string type and its constants. tether finds them;
the generated `tether_gen.go` registers them for the server.

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
