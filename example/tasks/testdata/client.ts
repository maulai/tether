// Drives the example server through the generated client: node client.ts <server url>
import assert from "node:assert/strict";
import { configure, ValidationError, ApiError } from "./runtime.ts";
import { createTask, getTask, listTasks, updateTask, deleteTask, exportTasks, importTasks, taskInputSchema, taskFilterSchema } from "./index.ts";

configure({ baseUrl: process.argv[2] ?? "" });

const task = await createTask({ title: "Write the README", status: "open", tags: ["docs"] });
assert.equal(task.title, "Write the README");
assert.deepEqual(task.tags, ["docs"]);

// The browser and the server report the same errors for the same input.
const bad = { title: " ", status: "blocked" as const, tags: ["", "x".repeat(31)] };
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
  ["title:required", "reason:requiredif", "tags.0:required", "tags.1:maxLength"],
);

// A handler's own field error arrives the same way.
try {
  await createTask({ title: "write the readme", status: "open" });
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
const done = await updateTask(task.id, { title: "Write the README", status: "done" });
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
    fetch(this.address, { method: this.method, headers: this.sent, body }).then(
      async (res) => {
        this.status = res.status;
        this.responseText = await res.text();
        this.received = res.headers;
        this.onload?.();
      },
      () => this.onerror?.(),
    );
  }
  abort() {}
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

console.log("client ok");
