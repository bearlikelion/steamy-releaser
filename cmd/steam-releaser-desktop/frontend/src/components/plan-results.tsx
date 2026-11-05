import type { CommandPlan, ReleasePlan } from "@/lib/types";

function CommandDetails({ command }: { command: CommandPlan }) {
  return (
    <div className="space-y-3">
      <dl className="space-y-2 text-xs">
        <div>
          <dt className="text-muted-foreground">Executable</dt>
          <dd className="break-all font-mono">{command.program}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Working directory</dt>
          <dd className="break-all font-mono">{command.working_dir}</dd>
        </div>
      </dl>
      <div>
        <p className="mb-1 text-xs font-medium">Arguments in order</p>
        <ol
          aria-label={`${command.label} arguments`}
          className="list-inside list-decimal space-y-1 text-xs"
        >
          {(command.args ?? []).map((arg, index) => (
            <li key={index} className="break-all font-mono">
              {JSON.stringify(arg)}
            </li>
          ))}
        </ol>
      </div>
      <div>
        <p className="mb-1 text-xs font-medium">Environment overrides</p>
        {Object.keys(command.environment ?? {}).length ? (
          <dl
            className="space-y-1 font-mono text-xs"
            aria-label={`${command.label} environment overrides`}
          >
            {Object.entries(command.environment ?? {})
              .sort(([a], [b]) => a.localeCompare(b))
              .map(([name, value]) => (
                <div key={name} className="flex flex-wrap gap-2">
                  <dt className="break-all">{name}</dt>
                  <dd className="break-all">{JSON.stringify(value)}</dd>
                </div>
              ))}
          </dl>
        ) : (
          <p className="text-xs text-muted-foreground">
            No environment overrides
          </p>
        )}
      </div>
      {command.warnings?.map((warning, index) => (
        <output key={index} className="block text-xs text-amber-300">
          {warning}
        </output>
      ))}
    </div>
  );
}

function parsePlan(result: string): ReleasePlan | null {
  try {
    const plan: Partial<ReleasePlan> | null = JSON.parse(result);
    if (
      !plan ||
      typeof plan.project !== "string" ||
      typeof plan.profile !== "string" ||
      typeof plan.engine !== "string" ||
      !Array.isArray(plan.targets) ||
      !plan.upload ||
      !plan.targets.every(
        (target) =>
          target &&
          typeof target.name === "string" &&
          typeof target.output === "string" &&
          typeof target.content === "string",
      )
    )
      return null;
    return plan as ReleasePlan;
  } catch {
    return null;
  }
}

export function PlanResults({ result }: { result: string }) {
  const plan = parsePlan(result);
  if (!plan)
    return (
      <pre className="font-mono text-xs whitespace-pre-wrap break-all">
        {result}
      </pre>
    );
  return (
    <div className="space-y-4">
      <div>
        <h3 className="font-semibold">
          {plan.project} / {plan.profile}
        </h3>
        <p className="text-xs text-muted-foreground">
          {plan.engine} · {plan.parallelism} simultaneous build processes
        </p>
        <p className="mt-1 text-sm">
          Steam App {plan.upload.app_id} · Depots{" "}
          {(plan.upload.depot_ids ?? []).join(", ")}
        </p>
        <p className="text-xs text-muted-foreground">
          {plan.upload.preview
            ? "Steam preview only"
            : plan.upload.set_live
              ? `Set live on beta branch ${plan.upload.set_live}`
              : "Upload without setting live"}
        </p>
      </div>
      {plan.prepare?.map((command, index) => (
        <section
          key={index}
          aria-label={`Preparation: ${command.label}`}
          className="space-y-3 rounded-lg border p-3"
        >
          <h4 className="text-sm font-semibold">
            Preparation: {command.label}
          </h4>
          <CommandDetails command={command} />
        </section>
      ))}
      {plan.targets.map((target) => (
        <section
          key={target.name}
          aria-label={`Build plan for ${target.name}`}
          className="space-y-3 rounded-lg border p-3"
        >
          <h4 className="text-sm font-semibold">
            {target.name} · Depot {target.depot_id}
          </h4>
          <dl className="space-y-2 text-xs">
            <div>
              <dt className="text-muted-foreground">Build output</dt>
              <dd className="break-all font-mono">{target.output}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Upload content</dt>
              <dd className="break-all font-mono">{target.content}</dd>
            </div>
          </dl>
          {target.command ? (
            <CommandDetails command={target.command} />
          ) : (
            <p className="text-xs text-muted-foreground">
              Uses existing artifacts
            </p>
          )}
        </section>
      ))}
      <details>
        <summary className="cursor-pointer text-xs text-muted-foreground">
          Raw plan
        </summary>
        <pre className="mt-2 font-mono text-xs whitespace-pre-wrap break-all">
          {result}
        </pre>
      </details>
    </div>
  );
}
