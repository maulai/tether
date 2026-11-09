// Command tasks is a small task list API built with tether: the standard
// library serves it, tether reads, validates and answers each request, and
// go generate writes the OpenAPI document and the TypeScript client in web.
package main

//go:generate go tool tether -out web/src/api -title Tasks -version 1.0.0

import (
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maulai/tether"
)

// Status is where a task stands.
type Status string

const (
	StatusOpen    Status = "open"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
)

// A rule of this project: the server checks it, and the generated client
// checks it in the browser with the same message.
func init() {
	tether.Pattern("tag", `[a-z0-9]+(?:-[a-z0-9]+)*`, "Use lower case letters, digits and dashes.")
}

// TaskInput is what a form sends to create or change a task.
type TaskInput struct {
	// Title is a short, unique name.
	Title  string `json:"title" v:"required,max=120" example:"Write the README"`
	Status Status `json:"status" v:"required"`
	// Reason says why a blocked task cannot go on.
	Reason string     `json:"reason" v:"requiredif=status:blocked,max=500"`
	Due    *time.Time `json:"due"`
	Tags   []string   `json:"tags" v:"max=5,dive,required,max=30,tag"`
}

// Task is a task as the API returns it.
type Task struct {
	ID int `json:"id"`
	TaskInput
	CreatedAt time.Time `json:"created_at"`
}

// TaskFilter narrows the list of tasks.
type TaskFilter struct {
	Status Status `json:"status"`
	// Tags keeps the tasks that have all of these tags.
	Tags []string `json:"tags" v:"dive,required"`
}

var (
	mu    sync.Mutex
	tasks = map[int]Task{}
	next  = 0
)

// ListTasks returns the tasks, oldest first.
func ListTasks(r *http.Request, f TaskFilter) ([]Task, error) {
	mu.Lock()
	defer mu.Unlock()
	var out []Task
	for _, t := range tasks {
		if (f.Status == "" || t.Status == f.Status) && hasAll(t.Tags, f.Tags) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Task) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

// GetTask returns one task.
func GetTask(r *http.Request, id int) (Task, error) {
	mu.Lock()
	defer mu.Unlock()
	return find(id)
}

// CreateTask adds a task.
func CreateTask(r *http.Request, in TaskInput) (Task, error) {
	mu.Lock()
	defer mu.Unlock()
	if taken(in.Title, 0) {
		return Task{}, tether.FieldError("title", "A task with this title exists already.")
	}
	next++
	t := Task{ID: next, TaskInput: in, CreatedAt: time.Now()}
	tasks[t.ID] = t
	return t, nil
}

// UpdateTask replaces a task's fields.
func UpdateTask(r *http.Request, id int, in TaskInput) (Task, error) {
	mu.Lock()
	defer mu.Unlock()
	t, err := find(id)
	if err != nil {
		return Task{}, err
	}
	if taken(in.Title, t.ID) {
		return Task{}, tether.FieldError("title", "A task with this title exists already.")
	}
	t.TaskInput = in
	tasks[t.ID] = t
	return t, nil
}

// DeleteTask removes a task.
func DeleteTask(r *http.Request, id int) error {
	mu.Lock()
	defer mu.Unlock()
	delete(tasks, id)
	return nil
}

// ImportTasks adds a task for every line of an uploaded text file.
func ImportTasks(r *http.Request, f tether.Upload) ([]Task, error) {
	data, err := io.ReadAll(io.LimitReader(f.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	var added []Task
	for _, line := range strings.Split(string(data), "\n") {
		title := strings.TrimSpace(line)
		if title == "" {
			continue
		}
		if taken(title, 0) {
			return nil, tether.FieldError("file", "A task named "+strconv.Quote(title)+" exists already.")
		}
		next++
		t := Task{ID: next, TaskInput: TaskInput{Title: title, Status: StatusOpen}, CreatedAt: time.Now()}
		tasks[t.ID] = t
		added = append(added, t)
	}
	return added, nil
}

// ExportTasks returns all tasks as CSV.
func ExportTasks(r *http.Request) (tether.File, error) {
	all, _ := ListTasks(r, TaskFilter{})
	var b strings.Builder
	b.WriteString("id,title,status\n")
	for _, t := range all {
		b.WriteString(strconv.Itoa(t.ID) + "," + strconv.Quote(t.Title) + "," + string(t.Status) + "\n")
	}
	return tether.File{Name: "tasks.csv", ContentType: "text/csv", Body: strings.NewReader(b.String())}, nil
}

func main() {
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", routes()))
}

// APIKey is the key a client sends in the X-API-Key header to change
// tasks. This example knows one key, demoKey; reading needs none.
var APIKey = tether.Auth{Header: "X-API-Key", Check: checkKey}

const demoKey = "demo"

// ErrNoKey answers a change without a valid key. The client recognises it
// with isNoKey.
var ErrNoKey = tether.Problem{Status: http.StatusUnauthorized, Code: "noKey", Message: "Send a valid key in X-API-Key."}

func checkKey(r *http.Request, _ []string) (*http.Request, error) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-API-Key")), []byte(demoKey)) != 1 {
		return nil, ErrNoKey
	}
	return r, nil
}

// Who may call a route: anyone, or a client with the key.
var (
	open   = tether.Options{Public: true}
	editor = tether.Options{Auth: APIKey}
)

// routes is the whole API: the standard library's router, with one
// tether.Route per endpoint. tether answers a panic in a handler itself;
// other middleware wraps the mux.
func routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(tether.Route("GET /v1/tasks", ListTasks, open))
	mux.Handle(tether.Route("GET /v1/tasks/{id}", GetTask, open))
	mux.Handle(tether.Route("POST /v1/tasks", CreateTask, editor))
	mux.Handle(tether.Route("PUT /v1/tasks/{id}", UpdateTask, editor))
	mux.Handle(tether.Route("DELETE /v1/tasks/{id}", DeleteTask, editor))
	mux.Handle(tether.Route("GET /v1/tasks.csv", ExportTasks, open))
	mux.Handle(tether.Route("POST /v1/tasks/import", ImportTasks, editor))
	return requestID(mux)
}

// requestID marks every response, so an error page can name the request.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", strconv.FormatInt(time.Now().UnixNano(), 36))
		next.ServeHTTP(w, r)
	})
}

// errNotFound is what the store reports for a missing task. The handlers
// return it as it is; MapErrors turns it into a 404 in one place.
var errNotFound = errors.New("task not found")

func init() {
	tether.MapErrors(func(err error) error {
		if errors.Is(err, errNotFound) {
			return tether.Fail(http.StatusNotFound, "No such task.")
		}
		return err
	})
}

// find returns a task. An id in the path that is not a number never gets
// here: tether answers it with 404 before the handler runs.
func find(id int) (Task, error) {
	t, ok := tasks[id]
	if !ok {
		return Task{}, errNotFound
	}
	return t, nil
}

func hasAll(have, want []string) bool {
	for _, tag := range want {
		if !slices.Contains(have, tag) {
			return false
		}
	}
	return true
}

func taken(title string, except int) bool {
	for id, t := range tasks {
		if id != except && strings.EqualFold(t.Title, title) {
			return true
		}
	}
	return false
}
