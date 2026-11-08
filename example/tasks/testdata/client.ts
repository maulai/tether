// Drives the example server through the generated client: node client.ts <server url>
import assert from "node:assert/strict";
import { configure, ValidationError, ApiError } from "./runtime.ts";
import { createTask, getTask, listTasks, updateTask, deleteTask, exportTasks, importTasks, taskInputSchema, taskFilterSchema } from "./index.ts";

configure({ baseUrl: process.argv[2] ?? "" });

// TaskInput is also what UpdateTask reads with PUT, so its type asks for
// every field: empty is "", null or [].
const task = await createTask({ title: "Write the README", status: "open", reason: "", due: null, tags: ["docs"] });
assert.equal(task.title, "Write the README");
assert.deepEqual(task.tags, ["docs"]);

// The browser and the server report the same errors for the same input.
const bad = { title: " ", status: "blocked" as const, reason: "", due: null, tags: ["", "x".repeat(31), "Docs"] };
const local = taskInputSchema.validate(bad);
try {
  await createTask(bad);
  assert.fail("expected a validation error");
} catch (e) {
  assert.ok(e instanceof ValidationError);
  assert.deepEqual(e.errors, local);
}
assert.deepEqual(
  local.map((e) => e.field + ":" + e.rule),
  ["title:required", "reason:requiredif", "tags.0:required", "tags.1:maxLength", "tags.2:tag"],
);
// A project rule shows its own message in the browser, as on the server.
assert.equal(local[4]?.message, "Use lower case letters, digits and dashes.");

// A handler's own field error arrives the same way.
try {
  await createTask({ title: "write the readme", status: "open", reason: "", due: null, tags: [] });
  assert.fail("expected a validation error");
} catch (e) {
  assert.ok(e instanceof ValidationError);
  assert.deepEqual(e.errors, [{ field: "title", message: "A task with this title exists already." }]);
}

assert.equal((await getTask(task.id)).id, task.id);
assert.equal((await listTasks({ status: "open" })).length, 1);
assert.equal((await listTasks({ status: "done" })).length, 0);
assert.equal((await listTasks({ tags: ["docs"] })).length, 1);

// A list in the query string keeps its empty elements, so the server
// reports them at the same position as the browser.
const filter = { tags: ["", "docs"] };
try {
  await listTasks(filter);
  assert.fail("expected a validation error");
} catch (e) {
  assert.ok(e instanceof ValidationError);
  assert.deepEqual(e.errors, taskFilterSchema.validate(filter));
  assert.equal(e.errors[0]?.field, "tags.0");
}
const done = await updateTask(task.id, { ...task, status: "done" });
assert.deepEqual(done.tags, ["docs"]); // sent along, so kept

// A PUT from outside the client that forgets fields is refused instead of
// clearing them.
const partial = await fetch(`${process.argv[2]}/v1/tasks/${task.id}`, {
  method: "PUT",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ title: "Write the README", status: "done" }),
});
assert.equal(partial.status, 422);
assert.deepEqual(
  (await partial.json()).errors.map((e: { field: string; rule: string }) => e.field + ":" + e.rule),
  ["due:missing", "reason:missing", "tags:missing"],
);
assert.equal(done.status, "done");
assert.match(await (await exportTasks()).text(), /Write the README/);
await deleteTask(task.id);
try {
  await getTask(task.id);
  assert.fail("expected a 404");
} catch (e) {
  assert.ok(e instanceof ApiError);
  assert.equal(e.status, 404);
  assert.equal(e.message, "No such task."); // from MapErrors
  assert.ok(e.headers.get("X-Request-ID")); // the response's headers
}

// An id that does not parse never reaches the store.
try {
  await getTask("seven");
  assert.fail("expected a 404");
} catch (e) {
  assert.ok(e instanceof ApiError);
  assert.equal(e.status, 404);
  assert.equal(e.message, "Not found.");
}
// Uploads with progress go through XMLHttpRequest, which Node lacks. This
// stand-in sends the request with fetch to the real server and hands the
// answer back the way a browser's XMLHttpRequest would.
class FakeXHR {
  status = 0;
  responseText = "";
  withCredentials = false;
  upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  private stop = new AbortController();
  private method = "";
  private address = "";
  private sent = new Headers();
  private received = new Headers();
  open(method: string, address: string) {
    this.method = method;
    this.address = address;
  }
  setRequestHeader(name: string, value: string) {
    this.sent.set(name, value);
  }
  getAllResponseHeaders() {
    return [...this.received].map(([k, v]) => `${k}: ${v}`).join("\r\n");
  }
  send(body: FormData) {
    this.upload.onprogress?.({ lengthComputable: true, loaded: 1, total: 1 });
    // Like a browser, an aborted request goes no further and reports only
    // the abort.
    if (this.stop.signal.aborted) return;
    fetch(this.address, { method: this.method, headers: this.sent, body, signal: this.stop.signal }).then(
      async (res) => {
        this.status = res.status;
        this.responseText = await res.text();
        this.received = res.headers;
        this.onload?.();
      },
      () => {
        if (!this.stop.signal.aborted) this.onerror?.();
      },
    );
  }
  abort() {
    if (this.stop.signal.aborted) return;
    this.stop.abort();
    this.onabort?.();
  }
}
(globalThis as unknown as { XMLHttpRequest: unknown }).XMLHttpRequest = FakeXHR;

const progress: number[] = [];
const added = await importTasks(new Blob(["Alpha\nBeta\n"]), { onProgress: (f) => progress.push(f) });
assert.deepEqual(added.map((t) => t.title), ["Alpha", "Beta"]);
assert.deepEqual(progress, [1]);
try {
  await importTasks(new Blob(["Alpha\n"]), { onProgress: () => {} });
  assert.fail("expected a validation error");
} catch (e) {
  assert.ok(e instanceof ValidationError);
  assert.equal(e.errors[0]?.field, "file");
  assert.ok(e.headers.get("X-Request-ID")); // read from getAllResponseHeaders
}
// Without onProgress the upload goes through fetch.
assert.equal((await importTasks(new Blob(["Gamma"]))).length, 1);

// Aborting an upload settles it with the signal's reason, as fetch does.
const controller = new AbortController();
await assert.rejects(
  importTasks(new Blob(["Delta"]), { signal: controller.signal, onProgress: () => controller.abort() }),
  { name: "AbortError" },
);
// A signal aborted before the call sends nothing, on both paths, and a
// timeout arrives as itself, not as an unreachable server.
const count = (await listTasks()).length;
const late = () => AbortSignal.abort(new DOMException("Too slow.", "TimeoutError"));
await assert.rejects(importTasks(new Blob(["Epsilon"]), { signal: AbortSignal.abort(), onProgress: () => {} }), { name: "AbortError" });
await assert.rejects(importTasks(new Blob(["Epsilon"]), { signal: late(), onProgress: () => {} }), { name: "TimeoutError" });
await assert.rejects(importTasks(new Blob(["Epsilon"]), { signal: late() }), { name: "TimeoutError" });
await assert.rejects(exportTasks({ signal: late() }), { name: "TimeoutError" });
assert.equal((await listTasks()).length, count);

console.log("client ok");
