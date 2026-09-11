import { describe, expect, it } from "vitest";
import type {
	WorkspaceCommandActivity,
	WorkspaceMCPRequestActivity,
} from "#/api/typesGenerated";
import {
	activityHistoricalRange,
	activityInput,
	buildActivityDisplayRows,
} from "./activityDisplay";

const activity = (
	id: string,
	startedAt: string,
	finishedAt: string | undefined,
	tool: string,
	status: WorkspaceCommandActivity["status"] = "succeeded",
): WorkspaceCommandActivity => ({
	id,
	agent_id: "00000000-0000-0000-0000-000000000001",
	session_id: "00000000-0000-0000-0000-000000000002",
	source: "mcp",
	kind: "tool",
	tool,
	command: "{}",
	argv: [],
	status,
	started_at: startedAt,
	finished_at: finishedAt,
});

const request = (
	id: string,
	startedAt: string,
	finishedAt?: string,
): WorkspaceMCPRequestActivity => ({
	id,
	status: finishedAt ? "succeeded" : "running",
	started_at: startedAt,
	finished_at: finishedAt,
});

describe("activityInput", () => {
	it("preserves argv boundaries including spaces and empty arguments", () => {
		const item: WorkspaceCommandActivity = {
			...activity(
				"00000000-0000-0000-0000-000000000001",
				"2026-09-09T10:00:00Z",
				"2026-09-09T10:00:01Z",
				"exec",
			),
			command: "printf",
			argv: ["hello world", "", "--flag=value"],
		};

		expect(activityInput(item)).toBe(
			'printf\nargv: ["hello world","","--flag=value"]',
		);
	});
});

describe("buildActivityDisplayRows", () => {
	it("derives historical Idle from MCP request spans instead of visible command gaps", () => {
		const newest = activity(
			"00000000-0000-0000-0000-000000000003",
			"2026-09-09T10:05:00Z",
			"2026-09-09T10:06:00Z",
			"read_file",
		);
		const oldest = activity(
			"00000000-0000-0000-0000-000000000005",
			"2026-09-09T10:00:00Z",
			"2026-09-09T10:01:00Z",
			"search_start",
		);
		const requests = [
			request(
				"00000000-0000-0000-0000-000000000101",
				"2026-09-09T10:00:00Z",
				"2026-09-09T10:01:00Z",
			),
			request(
				"00000000-0000-0000-0000-000000000102",
				"2026-09-09T10:02:00Z",
				"2026-09-09T10:03:00Z",
			),
			request(
				"00000000-0000-0000-0000-000000000103",
				"2026-09-09T10:05:00Z",
				"2026-09-09T10:06:00Z",
			),
		];

		const rows = buildActivityDisplayRows(
			[newest, oldest],
			requests,
			true,
			true,
			"started",
			"desc",
		);
		const idleRows = rows.filter((row) => row.type === "idle");
		expect(idleRows).toEqual([
			expect.objectContaining({
				current: true,
				startedAt: "2026-09-09T10:06:00.000Z",
			}),
			expect.objectContaining({
				current: false,
				startedAt: "2026-09-09T10:03:00.000Z",
				finishedAt: "2026-09-09T10:05:00.000Z",
			}),
			expect.objectContaining({
				current: false,
				startedAt: "2026-09-09T10:01:00.000Z",
				finishedAt: "2026-09-09T10:02:00.000Z",
			}),
		]);
	});

	it("merges parallel MCP requests before deriving Idle", () => {
		const visible = activity(
			"00000000-0000-0000-0000-000000000006",
			"2026-09-09T10:00:00Z",
			"2026-09-09T10:08:00Z",
			"bash",
		);
		const rows = buildActivityDisplayRows(
			[visible],
			[
				request(
					"00000000-0000-0000-0000-000000000111",
					"2026-09-09T10:00:00Z",
					"2026-09-09T10:05:00Z",
				),
				request(
					"00000000-0000-0000-0000-000000000112",
					"2026-09-09T10:02:00Z",
					"2026-09-09T10:03:00Z",
				),
				request(
					"00000000-0000-0000-0000-000000000113",
					"2026-09-09T10:07:00Z",
					"2026-09-09T10:08:00Z",
				),
			],
			true,
			true,
			"started",
			"desc",
		);
		const historicalIdle = rows.filter(
			(row) => row.type === "idle" && !row.current,
		);
		expect(historicalIdle).toEqual([
			expect.objectContaining({
				startedAt: "2026-09-09T10:05:00.000Z",
				finishedAt: "2026-09-09T10:07:00.000Z",
			}),
		]);
	});

	it("does not let an old pinned running process expand the historical Idle range", () => {
		const oldRunning = activity(
			"00000000-0000-0000-0000-000000000201",
			"2026-09-09T03:00:00Z",
			undefined,
			"process_start",
			"running",
		);
		const firstCompleted = activity(
			"00000000-0000-0000-0000-000000000202",
			"2026-09-09T10:00:00Z",
			"2026-09-09T10:00:01Z",
			"read_file",
		);
		const lastCompleted = activity(
			"00000000-0000-0000-0000-000000000203",
			"2026-09-09T10:05:00Z",
			"2026-09-09T10:05:01Z",
			"search_results",
		);
		expect(
			activityHistoricalRange([oldRunning, firstCompleted, lastCompleted]),
		).toEqual({
			rangeStart: Date.parse("2026-09-09T10:00:00Z"),
			rangeEnd: Date.parse("2026-09-09T10:05:01Z"),
		});
	});

	it("puts current Idle above a background running process after process_start returned", () => {
		const running = activity(
			"00000000-0000-0000-0000-000000000007",
			"2026-09-09T10:00:00Z",
			undefined,
			"process_start",
			"running",
		);
		const rows = buildActivityDisplayRows(
			[running],
			[
				request(
					"00000000-0000-0000-0000-000000000121",
					"2026-09-09T10:00:00Z",
					"2026-09-09T10:00:00.100Z",
				),
			],
			true,
			true,
			"started",
			"desc",
		);
		expect(rows[0]).toEqual(
			expect.objectContaining({
				type: "idle",
				current: true,
				startedAt: "2026-09-09T10:00:00.100Z",
			}),
		);
		expect(rows[1]).toEqual(
			expect.objectContaining({ type: "activity", activity: running }),
		);
	});

	it("suppresses current Idle while any MCP request is still waiting for a response", () => {
		const running = activity(
			"00000000-0000-0000-0000-000000000008",
			"2026-09-09T10:00:00Z",
			undefined,
			"bash",
			"running",
		);
		const rows = buildActivityDisplayRows(
			[running],
			[request("00000000-0000-0000-0000-000000000131", "2026-09-09T10:00:00Z")],
			true,
			true,
			"started",
			"desc",
		);
		expect(rows[0]).toEqual(
			expect.objectContaining({ type: "activity", activity: running }),
		);
		expect(rows.some((row) => row.type === "idle" && row.current)).toBe(false);
	});
});
