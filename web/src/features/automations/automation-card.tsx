import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import type { ComponentType, ReactNode } from "react";
import { Link } from "react-router-dom";

import {
  automationJsonSource,
  formatJsonSource,
} from "../../api/automation-json";
import { getAutomation } from "../../api/automations";
import { ApiError, useBaseUrlVersion } from "../../api/client";
import type { NotificationDestination } from "../../api/notifications";
import type { AutomationStep, Device, Entity } from "../../api/types";
import { Button } from "../../components/button";
import { Card } from "../../components/card";
import { SegmentedControl } from "../../components/segmented-control";
import { cn } from "../../lib/utils";
import { JsonCode } from "./automation-code";
import {
  ConditionText,
  entityLabelText,
  InitialVariables,
  SentenceSequence,
  StackSequence,
  TriggerText,
} from "./automation-read";
import type { ReadContext } from "./automation-read";
import {
  automationEntityIds,
  describeAutomationSteps,
} from "./definition-tree";
import { readAutomationDocument } from "./document";
import type { AutomationDocument, AutomationReadStyle } from "./document";
import { collection } from "./resources";

const paragraph = "my-[5px] text-label text-muted-foreground";
const heading = "m-0 text-body font-semibold";
const row = "flex flex-wrap items-center justify-between gap-3";
const action = "max-w-full";

interface Props {
  document: AutomationDocument;
  onEdit?: () => void;
  children?: ReactNode;
  automationId?: string;
  compact?: boolean;
  entityLabels?: ReadonlyMap<string, string>;
  headingLevel?: 2 | 3 | 4;
}
type ChildStatus =
  | { status: "loading" }
  | { status: "missing" }
  | { status: "error" }
  | { status: "ready"; name: string };
type ChildPreviewState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; document: AutomationDocument };

const referencedChildren = (steps: AutomationStep[]): string[] => {
  const ids = new Set<string>();
  const visit = (sequence: AutomationStep[]) => {
    for (const step of sequence) {
      if (step.kind === "sub_automation") {
        ids.add(step.automation_id);
      }
      if (step.kind === "if") {
        visit(step.then);
        if (step.else) {
          visit(step.else);
        }
      }
      if (step.kind === "choose") {
        for (const branch of step.branches) {
          visit(branch.steps);
        }
        if (step.default) {
          visit(step.default);
        }
      }
    }
  };
  visit(steps);
  return [...ids];
};

const CardContent = ({
  document,
  ancestry,
  entityLabels,
  ChildPreviewComponent,
}: {
  document: AutomationDocument;
  ancestry: string[];
  entityLabels?: ReadonlyMap<string, string>;
  ChildPreviewComponent: ComponentType<{
    id: string;
    close: () => void;
    ancestry: string[];
  }>;
}) => {
  const [style, setStyle] = useState<AutomationReadStyle>("sentence");
  const [child, setChild] = useState<string | null>(null);
  const [childNamesState, setChildNamesState] = useState<{
    key: string;
    names: Record<string, ChildStatus>;
  } | null>(null);
  const [referenceNames, setReferenceNames] = useState<{
    key: string;
    entities: Entity[];
    devices: Device[];
    destinations: NotificationDestination[];
  } | null>(null);
  const opener = useRef<HTMLButtonElement | null>(null);
  const backend = useBaseUrlVersion();
  const childIdsKey = JSON.stringify(
    referencedChildren(document.definition.steps)
  );
  const childrenKey = `${backend}:${childIdsKey}`;
  const childIds: string[] = JSON.parse(childIdsKey);
  const childNames: Record<string, ChildStatus> =
    childNamesState?.key === childrenKey
      ? childNamesState.names
      : Object.fromEntries(
          childIds.map((id) => [id, { status: "loading" as const }])
        );
  const needsEntityNames =
    !entityLabels && automationEntityIds(document.definition).length > 0;
  const needsDestinationNames =
    describeAutomationSteps(document.definition.steps).notificationCount > 0;
  const referenceKey = `${backend}:${needsEntityNames}:${needsDestinationNames}`;
  const referenceNamesForRender =
    referenceNames?.key === referenceKey
      ? referenceNames
      : { entities: [], devices: [], destinations: [] };
  const { entities, devices, destinations } = referenceNamesForRender;
  useEffect(() => {
    const controller = new AbortController();
    const ids: string[] = JSON.parse(childIdsKey);
    const initialNames: Record<string, ChildStatus> = Object.fromEntries(
      ids.map((id) => [id, { status: "loading" as const }])
    );
    const update = (id: string, status: ChildStatus) => {
      setChildNamesState((previous) => {
        const names =
          previous?.key === childrenKey ? previous.names : initialNames;
        return { key: childrenKey, names: { ...names, [id]: status } };
      });
    };
    const load = async (id: string) => {
      try {
        const value = await getAutomation(id, controller.signal);
        const valid = readAutomationDocument(
          automationJsonSource(value.definition)
        );
        if (!controller.signal.aborted) {
          update(id, { status: "ready", name: valid.definition.name });
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          update(id, {
            status:
              error instanceof ApiError && error.status === 404
                ? "missing"
                : "error",
          });
        }
      }
    };
    for (const id of ids) {
      void load(id);
    }
    return () => controller.abort();
  }, [childIdsKey, childrenKey]);
  useEffect(() => {
    let active = true;
    const load = async () => {
      const [entityValues, deviceValues, destinationValues] = await Promise.all(
        [
          needsEntityNames
            ? collection<Entity>("/v1/entities").catch((): Entity[] => [])
            : Promise.resolve<Entity[]>([]),
          needsEntityNames
            ? collection<Device>("/v1/devices").catch((): Device[] => [])
            : Promise.resolve<Device[]>([]),
          needsDestinationNames
            ? collection<NotificationDestination>(
                "/v1/notification-destinations"
              ).catch((): NotificationDestination[] => [])
            : Promise.resolve<NotificationDestination[]>([]),
        ]
      );
      if (active) {
        setReferenceNames({
          key: referenceKey,
          entities: entityValues,
          devices: deviceValues,
          destinations: destinationValues,
        });
      }
    };
    // Names are best effort: a failed read leaves each reference labeled by its ID.
    void load();
    return () => {
      active = false;
    };
  }, [needsDestinationNames, needsEntityNames, referenceKey]);
  const inspect = (id: string, button: HTMLButtonElement) => {
    opener.current = button;
    setChild(id);
  };
  const close = useCallback(() => {
    setChild(null);
    requestAnimationFrame(() => {
      const target = opener.current;
      if (!target) {
        return;
      }
      for (
        let parent = target.parentElement;
        parent;
        parent = parent.parentElement
      ) {
        if (parent instanceof HTMLDetailsElement) {
          parent.open = true;
        }
      }
      target.focus();
    });
  }, []);
  const ctx: ReadContext = {
    document,
    inspect,
    entityLabel: (id) => {
      if (entityLabels) {
        return entityLabels.get(id);
      }
      const found = entities.find((item) => item.id === id);
      return (
        found &&
        entityLabelText(
          found.name,
          devices.find((item) => item.id === found.device_id)?.name
        )
      );
    },
    destinationName: (id) => destinations.find((item) => item.id === id)?.name,
    childName: (id) => {
      const state = childNames[id];
      return state?.status === "ready" ? state.name : undefined;
    },
    childMissing: (id) => childNames[id]?.status === "missing",
  };
  const { definition } = document;
  return (
    <>
      <div className="mt-5">
        <span className="text-caption text-muted-foreground block leading-normal">
          When
        </span>
        <p className={paragraph}>
          {definition.triggers.length
            ? definition.triggers.map((trigger, index) => (
                <Fragment key={trigger.id}>
                  {index > 0 && "; "}
                  <TriggerText
                    ctx={ctx}
                    trigger={trigger}
                    path={["triggers", index]}
                  />{" "}
                  · Trigger {trigger.id}
                </Fragment>
              ))
            : "Manual only · no automatic Triggers"}
        </p>
        {definition.conditions && (
          <>
            <span className="text-caption text-muted-foreground mt-3 block leading-normal">
              Only if
            </span>
            <p className={paragraph}>
              <ConditionText
                ctx={ctx}
                condition={definition.conditions}
                path={["conditions"]}
              />
            </p>
          </>
        )}
        <InitialVariables ctx={ctx} />
      </div>
      <div
        className={`${row} border-border text-caption text-muted-foreground mt-4 border-t pt-3`}
      >
        <span>
          {definition.steps.length} root{" "}
          {definition.steps.length === 1 ? "Step" : "Steps"}
        </span>
        <SegmentedControl
          label="Read style"
          value={style}
          onValueChange={setStyle}
          disabled={!!child}
          size="touch"
          options={[
            { value: "sentence", label: "Sentence" },
            { value: "steps", label: "Steps" },
          ]}
        />
      </div>
      <div className="text-caption text-muted-foreground mt-[18px] block leading-normal">
        Then
      </div>
      {style === "sentence" ? (
        <SentenceSequence ctx={ctx} steps={definition.steps} path={["steps"]} />
      ) : (
        <StackSequence ctx={ctx} steps={definition.steps} path={["steps"]} />
      )}
      <details className="border-border border-t py-1">
        <summary className="marker:text-gray-7 min-h-9 cursor-pointer py-2">
          Exact definition
        </summary>
        <JsonCode source={formatJsonSource(document.source)} block />
      </details>
      {child &&
        (ancestry.includes(child) || ancestry.length >= 8 ? (
          <section className="border-gray-4 mt-4 border-t pt-4">
            <p className={paragraph}>
              Preview unavailable: recursive or deeply nested reference.
            </p>
            <Button
              variant="outline"
              size="touch"
              className={action}
              onClick={close}
            >
              Close preview
            </Button>
          </section>
        ) : (
          <ChildPreviewComponent
            key={child}
            id={child}
            close={close}
            ancestry={ancestry}
          />
        ))}
    </>
  );
};

const ChildPreview = ({
  id,
  close,
  ancestry,
}: {
  id: string;
  close: () => void;
  ancestry: string[];
}) => {
  const backend = useBaseUrlVersion();
  const requestKey = `${id}:${backend}`;
  const [loaded, setLoaded] = useState<{
    key: string;
    state: ChildPreviewState;
  } | null>(null);
  const state: ChildPreviewState =
    loaded?.key === requestKey ? loaded.state : { status: "loading" };
  const section = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const value = await getAutomation(id, controller.signal);
        const document = readAutomationDocument(
          automationJsonSource(value.definition)
        );
        if (!controller.signal.aborted) {
          setLoaded({
            key: requestKey,
            state: { status: "ready", document },
          });
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setLoaded({
            key: requestKey,
            state: {
              status: "error",
              message:
                error instanceof ApiError && error.status === 404
                  ? "Automation not found. It may have been removed."
                  : `Could not load Automation: ${error instanceof Error ? error.message : "Unknown error"}`,
            },
          });
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [id, requestKey]);
  useEffect(() => {
    const element = section.current;
    if (!element) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        close();
      }
    };
    element.addEventListener("keydown", handleKeyDown);
    return () => element.removeEventListener("keydown", handleKeyDown);
  }, [close]);
  return (
    <section
      ref={section}
      className="border-gray-4 mt-4 border-t pt-4"
      aria-label="Referenced Automation"
    >
      <div className={row}>
        <div>
          <h3 className={heading}>Referenced Automation</h3>
          <p className={paragraph}>Current definition · read-only</p>
        </div>
        <Button
          variant="outline"
          size="touch"
          className={action}
          autoFocus
          onClick={close}
        >
          Close preview
        </Button>
      </div>
      <Link
        className="text-violet-text focus-visible:outline-ring focus-visible:outline-2 focus-visible:outline-offset-3 focus-visible:outline-solid"
        to={`/automations/${encodeURIComponent(id)}`}
      >
        Open Automation and history
      </Link>
      {state.status === "loading" && (
        <output className={`${paragraph} block`}>Loading {id}…</output>
      )}
      {state.status === "error" && (
        <p className={`${paragraph} text-destructive`} role="alert">
          {state.message}
        </p>
      )}
      {state.status === "ready" && (
        <>
          <h4 className={heading}>{state.document.definition.name}</h4>
          <p className={paragraph}>{id}</p>
          <CardContent
            document={state.document}
            ancestry={[...ancestry, id]}
            ChildPreviewComponent={ChildPreview}
          />
        </>
      )}
    </section>
  );
};

export const AutomationCard = ({
  document,
  onEdit,
  children,
  automationId,
  compact = false,
  entityLabels,
  headingLevel = 3,
}: Props) => {
  const Heading = `h${headingLevel}` as const;
  let valid: AutomationDocument | undefined;
  let message = "Invalid definition";
  try {
    valid = readAutomationDocument(document.source);
  } catch (error) {
    if (error instanceof Error) {
      ({ message } = error);
    }
  }
  return (
    <Card
      asChild
      className={cn(
        "max-tablet:p-3.5 [overflow-wrap:anywhere]",
        compact && "p-3.5"
      )}
    >
      <article>
        {valid ? (
          <>
            <header className={row}>
              <div>
                <Heading className={heading}>{valid.definition.name}</Heading>
                <p className={paragraph}>
                  {valid.definition.enabled
                    ? "Automatic Runs enabled"
                    : "Automatic Runs disabled"}
                </p>
              </div>
              {onEdit && (
                <Button
                  size="touch"
                  variant="outline"
                  className={action}
                  onClick={onEdit}
                >
                  Edit Automation
                </Button>
              )}
            </header>
            <CardContent
              document={valid}
              ancestry={automationId ? [automationId] : []}
              entityLabels={entityLabels}
              ChildPreviewComponent={ChildPreview}
            />
          </>
        ) : (
          <p className={`${paragraph} text-destructive`} role="alert">
            This Automation cannot be shown: {message}
          </p>
        )}
        {children}
      </article>
    </Card>
  );
};
