import { useEffect, useId, useRef, useState } from "react";
import { apiFetch, getBaseUrl, useBaseUrlVersion } from "../api/client.ts";
import type { DevicePatch } from "../api/types.ts";
import { Button } from "./ui/button.tsx";
import { Input } from "./ui/input.tsx";
import { Label } from "./ui/label.tsx";

interface NamingMetadata {
  name: string;
  adapter_name: string;
  name_override: string | null;
}

interface Props {
  objectId: string;
  kind: "device" | "entity";
  metadata: NamingMetadata;
  onUpdated: (metadata: NamingMetadata) => void;
}

export default function NameEditor(props: Props) {
  const version = useBaseUrlVersion();
  return <Editor key={JSON.stringify([props.kind, props.objectId, version])} {...props} />;
}

function Editor({ objectId, kind, metadata, onUpdated }: Props) {
  const fieldId = useId();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState("");
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    return () => { active.current = false; };
  }, []);

  async function submit(override: string | null) {
    if (pending) return;
    const base = getBaseUrl();
    setPending(true);
    setError(null);
    setStatus("Saving name…");
    const body: DevicePatch = { name_edit: { override } };
    try {
      const updated = await apiFetch<NamingMetadata>(`/v1/${kind === "device" ? "devices" : "entities"}/${encodeURIComponent(objectId)}`, {
        method: "PATCH", body: JSON.stringify(body),
      });
      if (!active.current || getBaseUrl() !== base) return;
      onUpdated({ name: updated.name, adapter_name: updated.adapter_name, name_override: updated.name_override });
      setEditing(false);
      setStatus(override === null ? "Name reset." : "Name saved.");
    } catch (e) {
      if (!active.current || getBaseUrl() !== base) return;
      setError(e instanceof Error ? e.message : String(e));
      setStatus("");
    } finally {
      if (active.current && getBaseUrl() === base) setPending(false);
    }
  }

  return (
    <div className="mt-3 grid gap-2" aria-label={`${kind} name`}>
      <p className="text-sm">Display name: {metadata.name}</p>
      <p className="text-sm text-muted-foreground">Adapter name: {metadata.adapter_name}</p>
      <p className="text-sm">{metadata.name_override !== null ? "Name override active" : "Using Adapter name"}</p>
      {editing ? (
        <form className="grid gap-2" onSubmit={(event) => { event.preventDefault(); void submit(draft); }}>
          <Label htmlFor={fieldId}>Display name</Label>
          <Input id={fieldId} autoFocus value={draft} disabled={pending} aria-describedby={`${fieldId}-count`} onChange={(event) => setDraft(event.target.value)} />
          <p id={`${fieldId}-count`} className="text-xs text-muted-foreground">{Array.from(draft).length} code points. Names must contain 1–128 code points after trimming.</p>
          <div className="flex gap-2">
            <Button size="sm" type="submit" disabled={pending}>{pending ? "Saving…" : "Save name"}</Button>
            <Button size="sm" type="button" variant="outline" disabled={pending} onClick={() => { setEditing(false); setError(null); setStatus(""); }}>Cancel</Button>
          </div>
        </form>
      ) : <Button size="sm" variant="outline" disabled={pending} onClick={() => { setDraft(metadata.name); setEditing(true); setError(null); setStatus(""); }}>Rename</Button>}
      <Button size="sm" variant="outline" disabled={pending || metadata.name_override === null} onClick={() => void submit(null)}>Reset to Adapter name</Button>
      <p role="status" className="text-sm">{status}</p>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>
  );
}
