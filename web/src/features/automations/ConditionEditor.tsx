import { editAutomationDocument, documentValue, newDocumentID, type AutomationDocument } from "./document";
import { editJsonSource, type JsonPath } from "../../api/automation-json";
import type { Automation, AutomationBranchCondition, Entity } from "../../api/types";
import type { NotificationDestination } from "../../api/notifications";
import { isQuantityField, resolveQuantity, stateFields } from "../views/bindings/state-fields";
import type { AdapterFunction } from "./resources";

export type Resources = { entities: Entity[]; automations: Automation[]; destinations: NotificationDestination[]; functions: AdapterFunction[] };
export type Context = { document: AutomationDocument; resources: Resources; apply: (path: JsonPath, raw: string | undefined) => void; sequence: (path: JsonPath, update: (children: string[]) => string[]) => void; invalid: Record<string, string>; setInvalid: (key: string, value: string | null) => void };
const keyOf = (path: JsonPath) => JSON.stringify(path);
export const q = JSON.stringify;
export function Field({ ctx, path, label, raw = false, multiline = false }: { ctx: Context; path: JsonPath; label: string; raw?: boolean; multiline?: boolean }) {
  const key = keyOf(path); let source = "";
  try { source = documentValue(ctx.document, path); } catch { /* optional value */ }
  const shown = ctx.invalid[key] ?? (raw ? source : source ? String(JSON.parse(source)) : "");
  const update = (value: string) => {
    try { const next = editAutomationDocument(ctx.document, path, raw ? value : q(value)); ctx.setInvalid(key, null); ctx.apply([], next.source); }
    catch { ctx.setInvalid(key, value); }
  };
  const props = { value: shown, "aria-invalid": ctx.invalid[key] !== undefined, onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => update(event.target.value) };
  return <label className="automation-field"><span>{label}</span>{multiline ? <textarea {...props} rows={4}/> : <input {...props}/>} {ctx.invalid[key] !== undefined && <small role="alert">This value is invalid for this field{raw ? "; check its JSON and allowed range" : ""}.</small>}</label>;
}
export function Select({ ctx, path, label, options }: { ctx: Context; path: JsonPath; label: string; options: { value: string; label: string }[] }) {
  let current = ""; try { current = JSON.parse(documentValue(ctx.document, path)); } catch { /* absent */ }
  return <label className="automation-field"><span>{label}</span><select value={current} onChange={event => ctx.apply(path, q(event.target.value))}>{!options.some(option => option.value === current) && <option value={current}>{current || "Select…"}</option>}{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>;
}
export const entityOptions = (ctx: Context) => ctx.resources.entities.map(entity => ({ value: entity.id, label: `${entity.name} · ${entity.id}` }));
export const firstEntity = (ctx: Context) => ctx.resources.entities[0]?.id;
export function StateGuide({ ctx, entityId }: { ctx: Context; entityId: string }) {
  const entity = ctx.resources.entities.find(item => item.id === entityId);
  const fields = entity && Object.prototype.hasOwnProperty.call(stateFields, entity.type) ? stateFields[entity.type] : undefined;
  if (!entity || !fields) return <p>Use a JSON Pointer into the State value; canonical field guidance is unavailable for this Entity.</p>;
  return <div className="automation-guide"><span>State fields · canonical units</span><ul>{Object.entries(fields).map(([path, field]) => {
    let unit: string = field.value_type;
    if (isQuantityField(field)) { try { unit = resolveQuantity(field, entity.support).unit; } catch { unit = "unit unavailable from Entity support"; } }
    return <li key={path}><code>{path || '"" (root)'}</code> · {unit}{field.presence === "sometimes" ? " · may be absent" : ""}</li>;
  })}</ul></div>;
}
export function conditionSource(ctx: Context, kind: string, id: string): string | undefined {
  const entity = firstEntity(ctx); const childID = newDocumentID(ctx.document, "condition_child");
  if (kind === "entity_state") return entity ? q({ id, kind, entity_id: entity, value_pointer: "", operator: "eq", operand: true }) : undefined;
  if (kind === "expression") return q({ id, kind, expr: "true" });
  if (kind === "trigger") return ctx.document.definition.triggers[0] ? q({ id, kind, trigger_ids: [ctx.document.definition.triggers[0].id] }) : undefined;
  const child = conditionSource(ctx, "entity_state", childID);
  if (!child) return undefined;
  if (kind === "not") return `{"id":${q(id)},"kind":"not","child":${child}}`;
  if (kind === "none") return `{"id":${q(id)},"kind":"not","child":{"id":${q(childID)},"kind":"any","children":[${conditionSource(ctx, "entity_state", newDocumentID(ctx.document, "condition_leaf"))}]}}`;
  return `{"id":${q(id)},"kind":${q(kind)},"children":[${child}]}`;
}
export function Condition({ ctx, value, path, reached }: { ctx: Context; value: AutomationBranchCondition; path: JsonPath; reached: boolean }) {
  const none = value.kind === "not" && value.child.kind === "any";
  const kinds = ["entity_state", "all", "any", "none", "not", ...(reached ? ["expression", "trigger"] : [])];
  const changeKind = (kind: string) => {
    if ((value.kind === "all" || value.kind === "any") && (kind === "all" || kind === "any")) { ctx.apply([...path, "kind"], q(kind)); return; }
    if ((value.kind === "all" || value.kind === "any") && kind === "none") {
      const children = documentValue(ctx.document, [...path, "children"]);
      ctx.apply(path, `{"id":${q(value.id)},"kind":"not","child":{"id":${q(newDocumentID(ctx.document, "condition_group"))},"kind":"any","children":${children}}}`);
      return;
    }
    if (none && (kind === "all" || kind === "any")) {
      const children = documentValue(ctx.document, [...path, "child", "children"]);
      ctx.apply(path, `{"id":${q(value.id)},"kind":${q(kind)},"children":${children}}`);
      return;
    }
    if (["all", "any", "none", "not"].includes(kind)) {
      const child = editJsonSource(documentValue(ctx.document, path), ["id"], q(newDocumentID(ctx.document, "condition_child")));
      const group = kind === "none"
        ? `{"id":${q(value.id)},"kind":"not","child":{"id":${q(newDocumentID(ctx.document, "condition_group"))},"kind":"any","children":[${child}]}}`
        : kind === "not"
          ? `{"id":${q(value.id)},"kind":"not","child":${child}}`
          : `{"id":${q(value.id)},"kind":${q(kind)},"children":[${child}]}`;
      ctx.apply(path, group);
      return;
    }
    const source = conditionSource(ctx, kind, value.id); if (source) ctx.apply(path, source);
  };
  return <div className="automation-condition"><label className="automation-field"><span>Condition type</span><select value={none ? "none" : value.kind} onChange={event => changeKind(event.target.value)}>{kinds.map(kind => <option key={kind} value={kind}>{({ entity_state: "State", all: "All", any: "Any", none: "None (not any)", not: "Not", expression: "Expression", trigger: "Trigger" } as Record<string, string>)[kind]}</option>)}</select></label>
    {value.kind === "entity_state" && <><Select ctx={ctx} path={[...path, "entity_id"]} label="Entity" options={entityOptions(ctx)}/><Field ctx={ctx} path={[...path, "value_pointer"]} label="State value pointer"/><StateGuide ctx={ctx} entityId={value.entity_id}/><Select ctx={ctx} path={[...path, "operator"]} label="Comparison" options={["eq", "ne", "lt", "lte", "gt", "gte"].map(value => ({ value, label: value }))}/><Field ctx={ctx} path={[...path, "operand"]} label="Operand · exact JSON, canonical units" raw/></>}
    {value.kind === "expression" && <Field ctx={ctx} path={[...path, "expr"]} label="Expression"/>}
    {value.kind === "trigger" && <Field ctx={ctx} path={[...path, "trigger_ids"]} label="Trigger IDs · exact JSON array" raw/>}
    {(value.kind === "all" || value.kind === "any") && <><div className="automation-condition-list">{value.children.map((child, index) => <div key={child.id}><Condition ctx={ctx} value={child} path={[...path, "children", index]} reached={reached}/><button disabled={value.children.length === 1} onClick={() => ctx.sequence([...path, "children"], children => children.filter((_, i) => i !== index))}>Remove Condition</button></div>)}</div><button onClick={() => { const source = conditionSource(ctx, reached ? "expression" : "entity_state", newDocumentID(ctx.document, "condition")); if (source) ctx.sequence([...path, "children"], children => [...children, source]); }}>Add Condition</button></>}
    {value.kind === "not" && <Condition ctx={ctx} value={value.child} path={[...path, "child"]} reached={reached}/>}
  </div>;
}
