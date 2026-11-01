// Drives the example server through the generated client: node client.ts <server url>
import assert from "node:assert/strict";
import { configure, ValidationError, ApiError } from "./runtime.ts";
import { createTask, getTask, listTasks, updateTask, deleteTask, exportTasks, taskInputSchema } from "./index.ts";

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
  assert.equal(e.message, "No such task.");
}
console.log("client ok");
