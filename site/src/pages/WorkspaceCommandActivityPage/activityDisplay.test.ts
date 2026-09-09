import { describe, expect, it } from "vitest";
import type { WorkspaceCommandActivity } from "#/api/typesGenerated";
import { buildActivityDisplayRows } from "./activityDisplay";

const activity = (
	id: string,
	startedAt: string,
	finishedAt: string,
	tool: string,
): WorkspaceCommandActivity => ({
	id,
	agent_id: "00000000-0000-0000-0000-000000000001",
	session_id: "00000000-0000-0000-0000-000000000002",
	source: "mcp",
	kind: "tool",
	tool,
	command: "{}",
	argv: [],
	status: "succeeded",
	started_at: startedAt,
	finished_at: finishedAt,
});

describe("buildActivityDisplayRows", () => {
	it("derives Idle only from the real rows currently visible in the table", () => {
		const newest = activity(
			"00000000-0000-0000-0000-000000000003",
			"2026-09-09T10:05:00Z",
			"2026-09-09T10:06:00Z",
			"read_file",
		);
		const processOutput = activity(
			"00000000-0000-0000-0000-000000000004",
			"2026-09-09T10:02:00Z",
			"2026-09-09T10:03:00Z",
			"process_output",
		);
		const oldest = activity(
			"00000000-0000-0000-0000-000000000005",
			"2026-09-09T10:00:00Z",
			"2026-09-09T10:01:00Z",
			"search_start",
		);

		const withProcessOutput = buildActivityDisplayRows(
			[newest, processOutput, oldest],
			true,
			true,
		);
		expect(withProcessOutput.map((row) => row.type)).toEqual([
			"activity",
			"idle",
			"activity",
			"idle",
			"activity",
		]);
		const idleRows = withProcessOutput.filter((row) => row.type === "idle");
		expect(idleRows).toEqual([
			expect.objectContaining({
				startedAt: "2026-09-09T10:03:00.000Z",
				finishedAt: "2026-09-09T10:05:00.000Z",
			}),
			expect.objectContaining({
				startedAt: "2026-09-09T10:01:00.000Z",
				finishedAt: "2026-09-09T10:02:00.000Z",
			}),
		]);

		// Once process_output is filtered out, it no longer breaks the visible
		// idle interval. The browser recomputes one gap from the remaining rows.
		const withoutProcessOutput = buildActivityDisplayRows(
			[newest, oldest],
			true,
			true,
		);
		expect(withoutProcessOutput).toHaveLength(3);
		expect(withoutProcessOutput[1]).toEqual(
			expect.objectContaining({
				type: "idle",
				startedAt: "2026-09-09T10:01:00.000Z",
				finishedAt: "2026-09-09T10:05:00.000Z",
			}),
		);
	});

	it("can render only synthetic Idle rows while still using real rows as input", () => {
		const newest = activity(
			"00000000-0000-0000-0000-000000000006",
			"2026-09-09T10:02:00Z",
			"2026-09-09T10:03:00Z",
			"read_file",
		);
		const oldest = activity(
			"00000000-0000-0000-0000-000000000007",
			"2026-09-09T10:00:00Z",
			"2026-09-09T10:01:00Z",
			"search_start",
		);
		const rows = buildActivityDisplayRows([newest, oldest], true, false);
		expect(rows).toHaveLength(1);
		expect(rows[0].type).toBe("idle");
	});
});
