"use client";

import { Activity } from "lucide-react";
import type { WorkspaceExternalPresenceResponse } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";
import { useT } from "../../i18n";

interface WorkspaceExternalPresenceChipProps {
  presence: WorkspaceExternalPresenceResponse | undefined;
}

export function WorkspaceExternalPresenceChip({
  presence,
}: WorkspaceExternalPresenceChipProps) {
  const { t } = useT("issues");

  const unavailable = presence === undefined || presence.status === "unavailable";
  const rows = unavailable ? [] : presence.presence.filter((row) => row.activity_state !== "inactive");
  const active = rows.filter((row) => row.activity_state === "active");
  const waiting = rows.filter(
    (row) => row.activity_state === "waiting" || row.activity_state === "blocked",
  );
  const stale = rows.filter(
    (row) => row.activity_state === "stale" || row.activity_state === "unknown",
  );

  const activeExecutors = new Set(
    active.map((row) => row.executor_ref || row.host || row.source),
  ).size;

  const label = unavailable
    ? t(($) => $.external_presence.chip_unknown)
    : t(($) => $.external_presence.chip_active, { count: activeExecutors });

  const trigger = (
    <Badge
      variant={activeExecutors > 0 ? "secondary" : "outline"}
      className="h-7 shrink-0 gap-1.5 px-2 font-normal"
      aria-label={label}
    >
      <Activity className="size-3.5" />
      <span className="hidden md:inline">{label}</span>
      <span className="tabular-nums md:hidden">
        {unavailable ? "—" : activeExecutors}
      </span>
    </Badge>
  );

  return (
    <HoverCard>
      <HoverCardTrigger render={trigger} />
      <HoverCardContent align="end" className="w-80">
        {unavailable ? (
          <p className="text-caption text-muted-foreground">
            {t(($) => $.external_presence.unavailable_hover)}
          </p>
        ) : rows.length === 0 ? (
          <p className="text-caption text-muted-foreground">
            {t(($) => $.external_presence.empty_hover)}
          </p>
        ) : (
          <div className="flex flex-col gap-2">
            <div className="text-caption font-medium text-muted-foreground">
              {t(($) => $.external_presence.hover_header, { count: rows.length })}
            </div>
            <div className="flex flex-col gap-1.5">
              {rows.map((row, index) => {
                const stateKey =
                  row.activity_state === "active"
                    ? "active"
                    : row.activity_state === "waiting" || row.activity_state === "blocked"
                      ? "waiting"
                      : "stale";
                return (
                  <div
                    key={`${row.issue_id}:${row.executor_ref ?? row.host ?? row.source}:${row.run_id ?? index}`}
                    className="flex items-start gap-2 text-caption"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-medium">
                        {row.executor_ref || row.host || row.source}
                      </span>
                      <span className="block truncate text-muted-foreground">
                        {row.issue_identifier}
                      </span>
                    </span>
                    <span className="shrink-0 text-muted-foreground">
                      {t(($) => $.external_presence.state[stateKey])}
                    </span>
                  </div>
                );
              })}
            </div>
            {(waiting.length > 0 || stale.length > 0) && (
              <p className="text-micro text-muted-foreground">
                {t(($) => $.external_presence.non_active_note)}
              </p>
            )}
          </div>
        )}
      </HoverCardContent>
    </HoverCard>
  );
}
