// Command tasks is a small task list API built with tether: the standard
// library serves it, tether reads, validates and answers each request, and
// go generate writes the OpenAPI document and the TypeScript client in web.
package main

//go:generate go tool tether -out web/src/api -title Tasks -version 1.0.0

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
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
	ID string `json:"id"`
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
	tasks = map[string]Task{}
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
func GetTask(r *http.Request) (Task, error) {
	mu.Lock()
	defer mu.Unlock()
	return find(r)
}

// CreateTask adds a task.
func CreateTask(r *http.Request, in TaskInput) (Task, error) {
	mu.Lock()
	defer mu.Unlock()
	if taken(in.Title, "") {
		return Task{}, tether.FieldError("title", "A task with this title exists already.")
	}
	next++
	t := Task{ID: strconv.Itoa(next), TaskInput: in, CreatedAt: time.Now()}
	tasks[t.ID] = t
	return t, nil
}

// UpdateTask replaces a task's fields.
func UpdateTask(r *http.Request, in TaskInput) (Task, error) {
	mu.Lock()
	defer mu.Unlock()
	t, err := find(r)
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
func DeleteTask(r *http.Request) error {
	mu.Lock()
	defer mu.Unlock()
	delete(tasks, r.PathValue("id"))
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
		if taken(title, "") {
			return nil, tether.FieldError("file", "A task named "+strconv.Quote(title)+" exists already.")
		}
		next++
		t := Task{ID: strconv.Itoa(next), TaskInput: TaskInput{Title: title, Status: StatusOpen}, CreatedAt: time.Now()}
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
		b.WriteString(t.ID + "," + strconv.Quote(t.Title) + "," + string(t.Status) + "\n")
	}
	return tether.File{Name: "tasks.csv", ContentType: "text/csv", Body: strings.NewReader(b.String())}, nil
}

func main() {
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", routes()))
}

// routes is the whole API: the standard library's router, with one
// tether.Route per endpoint. Middleware would wrap the mux or a handler.
func routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(tether.Route("GET /v1/tasks", ListTasks))
	mux.Handle(tether.Route("GET /v1/tasks/{id}", GetTask))
	mux.Handle(tether.Route("POST /v1/tasks", CreateTask))
	mux.Handle(tether.Route("PUT /v1/tasks/{id}", UpdateTask))
	mux.Handle(tether.Route("DELETE /v1/tasks/{id}", DeleteTask))
	mux.Handle(tether.Route("GET /v1/tasks.csv", ExportTasks))
	mux.Handle(tether.Route("POST /v1/tasks/import", ImportTasks))
	// requestID goes outside, so a response to a panic carries the id too.
	return requestID(recoverPanics(mux))
}

// recoverPanics answers a panic like any other unexpected error: a 500
// problem document for the client, the cause and stack in the log.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if p == http.ErrAbortHandler {
				panic(p) // net/http's way to abort a response on purpose
			}
			tether.WriteError(w, r, fmt.Errorf("panic: %v\n%s", p, debug.Stack()))
		}()
		next.ServeHTTP(w, r)
	})
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

// find returns the task named by the path. PathValue parses the id; a value
// that is not a number answers 404 before the store is asked.
func find(r *http.Request) (Task, error) {
	id, err := tether.PathValue[int](r, "id")
	if err != nil {
		return Task{}, err
	}
	t, ok := tasks[strconv.Itoa(id)]
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

func taken(title, except string) bool {
	for id, t := range tasks {
		if id != except && strings.EqualFold(t.Title, title) {
			return true
		}
	}
	return false
}
