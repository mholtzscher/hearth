import { editAutomationDocument, documentValue, newDocumentID, type AutomationDocument } from "./document";
import { editJsonSource, type JsonPath } from "../../api/automation-json";
import type { Automation, AutomationBranchCondition, Entity } from "../../api/types";
import type { NotificationDestination } from "../../api/notifications";
import { isQuantityField, resolveQuantity, stateFields } from "../views/bindings/state-fields";
import type { AdapterFunction } from "./resources";
import { useId } from "react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { NativeSelect } from "../../components/ui/native-select";
import { Textarea } from "../../components/ui/textarea";

const field = "grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1 text-label";
const condition = "my-2 grid min-w-0 grid-cols-[minmax(0,1fr)] gap-2.5 border-l border-gray-5 pl-3 max-tablet:pl-[9px]";
const paragraph = "my-[5px] text-label text-muted-foreground";

export type Resources = { entities: Entity[]; automations: Automation[]; destinations: NotificationDestination[]; functions: AdapterFunction[] };
export type Context = { document: AutomationDocument; resources: Resources; apply: (path: JsonPath, raw: string | undefined) => void; sequence: (path: JsonPath, update: (children: string[]) => string[]) => void; invalid: Record<string, string>; setInvalid: (key: string, value: string | null) => void };
const keyOf = (path: JsonPath) => JSON.stringify(path);
export const q = JSON.stringify;
export function Field({ ctx, path, label, raw = false, multiline = false }: { ctx: Context; path: JsonPath; label: string; raw?: boolean; multiline?: boolean }) {
  const controlID = useId(), errorID = `${controlID}-error`;
  const key = keyOf(path); let source = "";
  try { source = documentValue(ctx.document, path); } catch { /* optional value */ }
  const shown = ctx.invalid[key] ?? (raw ? source : source ? String(JSON.parse(source)) : "");
  const blockedRootReplacement = path.length === 0 && Object.keys(ctx.invalid).some(other => other !== key);
  const update = (value: string) => {
    if (blockedRootReplacement) { ctx.setInvalid(key, value); return; }
    try { const next = editAutomationDocument(ctx.document, path, raw ? value : q(value)); ctx.setInvalid(key, null); ctx.apply([], next.source); }
    catch { ctx.setInvalid(key, value); }
  };
  const props = { id: controlID, value: shown, "aria-invalid": ctx.invalid[key] !== undefined, "aria-describedby": ctx.invalid[key] !== undefined ? errorID : undefined, onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => update(event.target.value) };
  return <div className={field}><label htmlFor={controlID}>{label}</label>{multiline ? <Textarea {...props} className="min-w-0 w-full resize-y font-mono" rows={4}/> : <Input {...props} size="auto" className="min-w-0 w-full"/>} {ctx.invalid[key] !== undefined && <small id={errorID} role="alert" className="text-destructive">{blockedRootReplacement ? "Fix other invalid fields before replacing the complete definition. Your replacement text is retained here." : `This value is invalid for this field${raw ? "; check its JSON and allowed range" : ""}.`}</small>}</div>;
}
export function Select({ ctx, path, label, options }: { ctx: Context; path: JsonPath; label: string; options: { value: string; label: string }[] }) {
  let current = ""; try { current = JSON.parse(documentValue(ctx.document, path)); } catch { /* absent */ }
  return <label className={field}><span>{label}</span><NativeSelect size="auto" className="min-w-0 w-full" value={current} onChange={event => ctx.apply(path, q(event.target.value))}>{!options.some(option => option.value === current) && <option value={current}>{current || "Select…"}</option>}{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</NativeSelect></label>;
}
export const entityOptions = (ctx: Context) => ctx.resources.entities.map(entity => ({ value: entity.id, label: `${entity.name} · ${entity.id}` }));
export const firstEntity = (ctx: Context) => ctx.resources.entities[0]?.id;
export function StateGuide({ ctx, entityId }: { ctx: Context; entityId: string }) {
  const entity = ctx.resources.entities.find(item => item.id === entityId);
  const fields = entity && Object.prototype.hasOwnProperty.call(stateFields, entity.type) ? stateFields[entity.type] : undefined;
  if (!entity || !fields) return <p className={paragraph}>Use a JSON Pointer into the State value; canonical field guidance is unavailable for this Entity.</p>;
  return <div className="text-caption text-muted-foreground"><span>State fields · canonical units</span><ul className="pl-[18px]">{Object.entries(fields).map(([path, field]) => {
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
  return <div className={condition}><label className={field}><span>Condition type</span><NativeSelect size="auto" className="min-w-0 w-full" value={none ? "none" : value.kind} onChange={event => changeKind(event.target.value)}>{kinds.map(kind => <option key={kind} value={kind}>{({ entity_state: "State", all: "All", any: "Any", none: "None (not any)", not: "Not", expression: "Expression", trigger: "Trigger" } as Record<string, string>)[kind]}</option>)}</NativeSelect></label>
    {value.kind === "entity_state" && <><Select ctx={ctx} path={[...path, "entity_id"]} label="Entity" options={entityOptions(ctx)}/><Field ctx={ctx} path={[...path, "value_pointer"]} label="State value pointer"/><StateGuide ctx={ctx} entityId={value.entity_id}/><Select ctx={ctx} path={[...path, "operator"]} label="Comparison" options={["eq", "ne", "lt", "lte", "gt", "gte"].map(value => ({ value, label: value }))}/><Field ctx={ctx} path={[...path, "operand"]} label="Operand · exact JSON, canonical units" raw/>{value.max_age_seconds === undefined ? <Button variant="outline" size="default" className="max-w-full max-tablet:h-11" onClick={() => ctx.apply([...path, "max_age_seconds"], "60")}>Add maximum State age</Button> : <><Field ctx={ctx} path={[...path, "max_age_seconds"]} label="Maximum State age · seconds" raw/><p className={paragraph}>Use 1–2,592,000 seconds. Older evidence is unknown, not false.</p><Button variant="outline" size="default" className="max-w-full max-tablet:h-11" onClick={() => ctx.apply([...path, "max_age_seconds"], undefined)}>Remove maximum State age</Button></>}</>}
    {value.kind === "expression" && <Field ctx={ctx} path={[...path, "expr"]} label="Expression"/>}
    {value.kind === "trigger" && <Field ctx={ctx} path={[...path, "trigger_ids"]} label="Trigger IDs · exact JSON array" raw/>}
    {(value.kind === "all" || value.kind === "any") && <><div className="grid grid-cols-[minmax(0,1fr)] gap-2.5">{value.children.map((child, index) => <div key={child.id}><Condition ctx={ctx} value={child} path={[...path, "children", index]} reached={reached}/><Button variant="outline" size="default" className="max-w-full max-tablet:h-11" disabled={value.children.length === 1} onClick={() => ctx.sequence([...path, "children"], children => children.filter((_, i) => i !== index))}>Remove Condition</Button></div>)}</div><Button variant="outline" size="default" className="max-w-full max-tablet:h-11" onClick={() => { const source = conditionSource(ctx, reached ? "expression" : "entity_state", newDocumentID(ctx.document, "condition")); if (source) ctx.sequence([...path, "children"], children => [...children, source]); }}>Add Condition</Button></>}
    {value.kind === "not" && <Condition ctx={ctx} value={value.child} path={[...path, "child"]} reached={reached}/>}
  </div>;
}
