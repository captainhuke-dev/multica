// @vitest-environment jsdom

import { cleanup, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceExternalPresenceResponse } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

vi.mock("@multica/ui/components/ui/hover-card", () => ({
  HoverCard: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  HoverCardTrigger: ({ render }: { render: React.ReactElement }) => render,
  HoverCardContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="hover-content">{children}</div>
  ),
}));

import { WorkspaceExternalPresenceChip } from "./workspace-external-presence-chip";

function presence(
  rows: WorkspaceExternalPresenceResponse["presence"],
): WorkspaceExternalPresenceResponse {
  return { status: "ok", presence: rows };
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("WorkspaceExternalPresenceChip", () => {
  it("keeps unresolved presence distinct from a known empty snapshot", () => {
    const first = renderWithI18n(
      <WorkspaceExternalPresenceChip presence={undefined} />,
    );

    expect(screen.getByLabelText("External work: —")).toBeTruthy();
    expect(screen.getByText("External execution presence is unavailable")).toBeTruthy();

    first.unmount();
    renderWithI18n(<WorkspaceExternalPresenceChip presence={presence([])} />);

    expect(screen.getByLabelText("0 external active")).toBeTruthy();
    expect(screen.getByText("No external execution evidence right now")).toBeTruthy();
  });

  it("counts distinct active executors without turning evidence rows into agents", () => {
    renderWithI18n(
      <WorkspaceExternalPresenceChip
        presence={presence([
          {
            issue_id: "issue-1",
            issue_identifier: "MCIT-508",
            executor_ref: "gpt-sol",
            kind: "controltower_executor",
            source: "controltower_execution_lease",
            run_id: "run-1",
            activity_state: "active",
          },
          {
            issue_id: "issue-2",
            issue_identifier: "MCIT-509",
            executor_ref: "gpt-sol",
            kind: "controltower_executor",
            source: "controltower_execution_lease",
            run_id: "run-2",
            activity_state: "active",
          },
          {
            issue_id: "issue-3",
            issue_identifier: "MCIT-510",
            executor_ref: "claude",
            kind: "controltower_executor",
            source: "controltower_execution_lease",
            run_id: "run-3",
            activity_state: "active",
          },
        ])}
      />,
    );

    expect(screen.getByLabelText("2 external active")).toBeTruthy();
    expect(screen.getAllByText("gpt-sol")).toHaveLength(2);
    expect(screen.getByText("claude")).toBeTruthy();
    expect(screen.getByText("MCIT-508")).toBeTruthy();
  });

  it("shows waiting and stale evidence but excludes it from the active count", () => {
    renderWithI18n(
      <WorkspaceExternalPresenceChip
        presence={presence([
          {
            issue_id: "issue-wait",
            issue_identifier: "MCIT-600",
            executor_ref: "gpt-sol",
            kind: "controltower_executor",
            source: "controltower_execution_lease",
            activity_state: "waiting",
          },
          {
            issue_id: "issue-stale",
            issue_identifier: "MCIT-601",
            executor_ref: "claude",
            kind: "controltower_executor",
            source: "controltower_execution_lease",
            activity_state: "stale",
          },
        ])}
      />,
    );

    expect(screen.getByLabelText("0 external active")).toBeTruthy();
    expect(screen.getByText("Waiting")).toBeTruthy();
    expect(screen.getByText("Stale")).toBeTruthy();
    expect(
      screen.getByText(
        "Waiting or stale evidence is shown for visibility only and does not imply execution authority.",
      ),
    ).toBeTruthy();
  });

  it("treats an explicit unavailable snapshot as unknown, not zero", () => {
    renderWithI18n(
      <WorkspaceExternalPresenceChip
        presence={{ status: "unavailable", presence: [] }}
      />,
    );

    expect(screen.getByLabelText("External work: —")).toBeTruthy();
    expect(screen.queryByLabelText("0 external active")).toBeNull();
  });
});
