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

const vectors: { formats?: Record<string, { type: string }>; cases: VectorCase[] } = JSON.parse(readFileSync(process.argv[2], "utf8"));
// The format of each vector type that has one, such as code.
const formats = Object.fromEntries(Object.entries(vectors.formats ?? {}).map(([name, f]) => [f.type, name]));

// typeSpec reads a vector type from the outside in, like the Go side: *T,
// []T and map[string]T around the base types.
function typeSpec(t: string, f: VectorField): FieldSpec {
  if (t === "[]byte") return { type: "bytes" };
  if (t === "map[color]string") return { type: "record", keys: colors, elem: { type: "string" } };
  if (t.startsWith("*")) return { ...typeSpec(t.slice(1), f), nullable: true };
  if (t.startsWith("[]")) return { type: "array", elem: typeSpec(t.slice(2), f) };
  if (t.startsWith("map[string]")) return { type: "record", elem: typeSpec(t.slice("map[string]".length), f) };
  const format = formats[t];
  if (format) return { type: "string", format };
  switch (t) {
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
  }
  throw new Error("unknown vector type " + f.type);
}

function spec(f: VectorField): FieldSpec {
  const s = typeSpec(f.type, f);
  return f.tag ? { ...s, v: f.tag } : s;
}

function specs(fields: VectorField[]): Record<string, FieldSpec> {
  return Object.fromEntries(fields.map((f) => [f.name, spec(f)]));
}

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
