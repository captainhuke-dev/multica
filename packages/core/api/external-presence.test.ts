// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

async function read(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status: 200 })),
  );
  return new ApiClient("https://api.example.test").getWorkspaceExternalPresence();
}

describe("external presence API compatibility", () => {
  it("preserves a valid external presence projection", async () => {
    expect(
      await read({
        status: "ok",
        presence: [
          {
            issue_id: "issue-1",
            issue_identifier: "MCIT-508",
            executor_ref: "gpt-sol",
            kind: "controltower_executor",
            source: "controltower_execution_lease",
            run_id: "run-1",
            generation: 2,
            pipeline_state: "EXECUTING",
            activity_state: "active",
            heartbeat_at: "2026-10-04T13:00:00Z",
          },
        ],
      }),
    ).toEqual({
      status: "ok",
      presence: [
        {
          issue_id: "issue-1",
          issue_identifier: "MCIT-508",
          executor_ref: "gpt-sol",
          kind: "controltower_executor",
          source: "controltower_execution_lease",
          run_id: "run-1",
          generation: 2,
          pipeline_state: "EXECUTING",
          activity_state: "active",
          heartbeat_at: "2026-10-04T13:00:00Z",
        },
      ],
    });
  });

  it.each([
    { status: "ok", presence: "not-an-array" },
    { status: "working", presence: [] },
    {
      status: "ok",
      presence: [
        {
          issue_id: "issue-1",
          issue_identifier: "MCIT-508",
          kind: "controltower_executor",
          source: "controltower_execution_lease",
          activity_state: "new-state-from-future-server",
        },
      ],
    },
  ])("fails closed for malformed or unknown projection state: %j", async (body) => {
    expect(await read(body)).toEqual({ status: "unavailable", presence: [] });
  });

  it("drops malformed additive metadata without hiding valid activity", async () => {
    expect(
      await read({
        status: "ok",
        presence: [
          {
            issue_id: "issue-1",
            issue_identifier: "MCIT-508",
            executor_ref: "gpt-sol",
            kind: "controltower_executor",
            source: "controltower_execution_lease",
            generation: "bad",
            activity_state: "active",
          },
        ],
      }),
    ).toEqual({
      status: "ok",
      presence: [
        {
          issue_id: "issue-1",
          issue_identifier: "MCIT-508",
          executor_ref: "gpt-sol",
          kind: "controltower_executor",
          source: "controltower_execution_lease",
          activity_state: "active",
        },
      ],
    });
  });
});
