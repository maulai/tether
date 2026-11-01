// Command tasks is a small task list API built with tether: the standard
// library serves it, tether reads, validates and answers each request, and
// go generate writes the OpenAPI document and the TypeScript client in web.
package main

//go:generate go tool tether -out web/src/api -title Tasks -version 1.0.0

import (
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

// TaskInput is what a form sends to create or change a task.
type TaskInput struct {
	// Title is a short, unique name.
	Title  string `json:"title" v:"required,max=120" example:"Write the README"`
	Status Status `json:"status" v:"required"`
	// Reason says why a blocked task cannot go on.
	Reason string     `json:"reason" v:"requiredif=status:blocked,max=500"`
	Due    *time.Time `json:"due"`
	Tags   []string   `json:"tags" v:"max=5,dive,required,max=30"`
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
	Tag    string `json:"tag"`
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
		if (f.Status == "" || t.Status == f.Status) && (f.Tag == "" || slices.Contains(t.Tags, f.Tag)) {
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
	t, ok := tasks[r.PathValue("id")]
	if !ok {
		return Task{}, tether.Fail(http.StatusNotFound, "No such task.")
	}
	return t, nil
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
	t, ok := tasks[r.PathValue("id")]
	if !ok {
		return Task{}, tether.Fail(http.StatusNotFound, "No such task.")
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
func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(tether.Route("GET /v1/tasks", ListTasks))
	mux.Handle(tether.Route("GET /v1/tasks/{id}", GetTask))
	mux.Handle(tether.Route("POST /v1/tasks", CreateTask))
	mux.Handle(tether.Route("PUT /v1/tasks/{id}", UpdateTask))
	mux.Handle(tether.Route("DELETE /v1/tasks/{id}", DeleteTask))
	mux.Handle(tether.Route("GET /v1/tasks.csv", ExportTasks))
	return mux
}

func taken(title, except string) bool {
	for id, t := range tasks {
		if id != except && strings.EqualFold(t.Title, title) {
			return true
		}
	}
	return false
}
