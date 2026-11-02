import type { Resolved } from "../bindings/types";

export function ResourceStatus({ resource }: { resource: Resolved<unknown> }) {
  const labels = { loading: "Loading…", missing: "Entity missing", error: "Request failed", unobserved: "Never observed", ready: "" };
  return <div className="view-resource-status">
    {labels[resource.status] && <span>{labels[resource.status]}</span>}
    {resource.unobserved && resource.status !== "unobserved" && <span>Never observed</span>}
    {resource.metadata && <>
      {!resource.metadata.enabled && <span>Disabled</span>}
      {resource.metadata.availability === "unavailable" && <span>Unavailable</span>}
      {resource.metadata.availability !== "available" && resource.metadata.availability !== "unavailable" && <span>Availability unknown</span>}
    </>}
    {resource.refreshFailed && <span>Refresh failed · showing last successful data</span>}
    {resource.status === "error" && resource.error && <span>{resource.error}</span>}
  </div>;
}
