// Runs the shared conformance vectors against the generated runtime. The Go
// test in this package writes runtime.ts next to this file and starts it
// with Node.js: node conformance.ts <vectors.json>
import { readFileSync } from "node:fs";
import { schema, type FieldSpec } from "./runtime.ts";

interface VectorField {
  name: string;
  type: string;
  tag: string;
  fields?: VectorField[];
}

// message is compared only where a case gives it.
interface VectorError {
  field: string;
  rule: string;
  message?: string;
}

interface VectorCase {
  name: string;
  fields: VectorField[];
  input: unknown;
  errors: VectorError[];
  browser?: VectorError[];
}

const colors = ["red", "green", "blue"];

function base(f: VectorField): FieldSpec {
  if (f.type === "[]byte") return { type: "bytes" };
  if (f.type === "map[color]string") return { type: "record", keys: colors, elem: { type: "string" } };
  switch (f.type.replace(/^\*/, "").replace(/^\[\]/, "")) {
    case "string":
      return { type: "string" };
    case "int":
      return { type: "number", int: "int" };
    case "int8":
      return { type: "number", int: "int8" };
    case "uint":
      return { type: "number", int: "uint" };
    case "float64":
      return { type: "number" };
    case "bool":
      return { type: "boolean" };
    case "time":
      return { type: "datetime" };
    case "color":
      return { type: "string", enum: colors };
    case "object":
      return { type: "object", fields: specs(f.fields ?? []) };
    case "map[string]string":
      return { type: "record", elem: { type: "string" } };
  }
  throw new Error("unknown vector type " + f.type);
}

function spec(f: VectorField): FieldSpec {
  let s = base(f);
  if (f.type.startsWith("[]") && f.type !== "[]byte") s = { type: "array", elem: s };
  if (f.type.startsWith("*")) s = { ...s, nullable: true };
  return f.tag ? { ...s, v: f.tag } : s;
}

function specs(fields: VectorField[]): Record<string, FieldSpec> {
  return Object.fromEntries(fields.map((f) => [f.name, spec(f)]));
}

const vectors: { cases: VectorCase[] } = JSON.parse(readFileSync(process.argv[2], "utf8"));
let failed = 0;
for (const c of vectors.cases) {
  const want = c.browser ?? c.errors;
  const got = schema(specs(c.fields))
    .validate(c.input)
    .map((e, i) => (want[i]?.message ? { field: e.field, rule: e.rule, message: e.message } : { field: e.field, rule: e.rule }));
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    failed++;
    console.log(`FAIL ${c.name}\n  got  ${JSON.stringify(got)}\n  want ${JSON.stringify(want)}`);
  }
}
console.log(`${vectors.cases.length - failed} of ${vectors.cases.length} cases pass`);
process.exit(failed ? 1 : 0);
