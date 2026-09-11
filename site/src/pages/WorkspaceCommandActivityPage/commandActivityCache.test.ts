import { describe, expect, it } from "vitest";
import type {
	WorkspaceCommandActivity,
	WorkspaceCommandActivityRequest,
	WorkspaceCommandActivityResponse,
	WorkspaceMCPRequestActivity,
} from "#/api/typesGenerated";
import { buildActivityDisplayRows } from "./activityDisplay";
import {
	commandMatchesRequest,
	updateCommandActivityCache,
	updateMCPRequestActivityCache,
} from "./commandActivityCache";

const request = {
	statuses: ["running", "succeeded", "failed", "interrupted"],
	sort_by: "started",
	sort_direction: "desc",
	page: 1,
	page_size: 2,
} satisfies WorkspaceCommandActivityRequest;

const response = {
	activity: [],
	total_count: 0,
	deletable_count: 0,
	available_tools: ["exec", "process_output"],
	page: 1,
	page_size: 2,
	total_pages: 0,
	history_limit: 0,
} satisfies WorkspaceCommandActivityResponse;

const running = {
	id: "00000000-0000-0000-0000-000000000001",
	agent_id: "00000000-0000-0000-0000-000000000002",
	session_id: "00000000-0000-0000-0000-000000000003",
	source: "mcp",
	tool: "exec",
	argv: ["sleep", "10"],
	status: "running",
	started_at: "2026-09-09T12:00:00Z",
} satisfies WorkspaceCommandActivity;

describe("updateCommandActivityCache", () => {
	it("prepends a new running command on the default realtime page without a REST resync", () => {
		const updated = updateCommandActivityCache(response, running, request);
		expect(updated).toBeDefined();
		expect(updated?.activity).toEqual([running]);
		expect(updated?.total_count).toBe(1);
		expect(updated?.deletable_count).toBe(1);
		expect(updated?.total_pages).toBe(1);
	});

	it("requests a REST resync when a pinned running command finishes", () => {
		const started = updateCommandActivityCache(response, running, request);
		expect(started).toBeDefined();
		const finished = {
			...running,
			status: "succeeded",
			finished_at: "2026-09-09T12:00:10Z",
			exit_code: 0,
		} satisfies WorkspaceCommandActivity;
		const updated = updateCommandActivityCache(started!, finished, request);
		expect(updated).toBeUndefined();
	});

	it("adds a newly observed MCP tool to the realtime dropdown without a REST resync", () => {
		const filteredRequest = {
			...request,
			tools: ["exec"],
		} satisfies WorkspaceCommandActivityRequest;
		const toolOnly = {
			...running,
			id: "00000000-0000-0000-0000-000000000010",
			kind: "tool",
			tool: "read_file",
			argv: [],
		} satisfies WorkspaceCommandActivity;
		const updated = updateCommandActivityCache(
			response,
			toolOnly,
			filteredRequest,
		);
		expect(updated).toBeDefined();
		expect(updated?.activity).toEqual([]);
		expect(updated?.available_tools).toEqual([
			"exec",
			"process_output",
			"read_file",
		]);
	});

	it("updates MCP request spans only for queries that include Idle", () => {
		const idleRequest = {
			...request,
			include_idle: true,
		} satisfies WorkspaceCommandActivityRequest;
		const requestStarted = {
			id: "00000000-0000-0000-0000-000000000101",
			status: "running",
			started_at: "2026-09-09T12:00:00Z",
		} satisfies WorkspaceMCPRequestActivity;
		const started = updateMCPRequestActivityCache(
			response,
			requestStarted,
			idleRequest,
		);
		expect(started.mcp_requests).toEqual([requestStarted]);

		const requestFinished = {
			...requestStarted,
			status: "succeeded",
			finished_at: "2026-09-09T12:00:01Z",
		} satisfies WorkspaceMCPRequestActivity;
		const finished = updateMCPRequestActivityCache(
			started,
			requestFinished,
			idleRequest,
		);
		expect(finished.mcp_requests).toEqual([requestFinished]);
		expect(
			updateMCPRequestActivityCache(response, requestStarted, request),
		).toBe(response);
	});

	it("preserves every MCP span required for the page when a live delta arrives", () => {
		const first = {
			...running,
			id: "00000000-0000-0000-0000-000000000201",
			status: "succeeded",
			started_at: "2026-09-09T10:00:00Z",
			finished_at: "2026-09-09T10:00:01Z",
			exit_code: 0,
		} satisfies WorkspaceCommandActivity;
		const last = {
			...running,
			id: "00000000-0000-0000-0000-000000000202",
			status: "succeeded",
			started_at: "2026-09-09T10:10:00Z",
			finished_at: "2026-09-09T10:10:01Z",
			exit_code: 0,
		} satisfies WorkspaceCommandActivity;
		const span = (
			id: string,
			started_at: string,
			finished_at?: string,
		): WorkspaceMCPRequestActivity => ({
			id,
			status: finished_at ? "succeeded" : "running",
			started_at,
			finished_at,
		});
		const before = {
			...response,
			activity: [last, first],
			mcp_requests: [
				span(
					"00000000-0000-0000-0000-000000000210",
					"2026-09-09T08:00:00Z",
					"2026-09-09T08:00:01Z",
				),
				span(
					"00000000-0000-0000-0000-000000000211",
					"2026-09-09T09:59:00Z",
					"2026-09-09T09:59:59Z",
				),
				span(
					"00000000-0000-0000-0000-000000000212",
					"2026-09-09T10:00:00Z",
					"2026-09-09T10:01:00Z",
				),
				span(
					"00000000-0000-0000-0000-000000000213",
					"2026-09-09T10:05:00Z",
					"2026-09-09T10:06:00Z",
				),
				span(
					"00000000-0000-0000-0000-000000000214",
					"2026-09-09T10:12:00Z",
					"2026-09-09T10:12:01Z",
				),
			],
		} satisfies WorkspaceCommandActivityResponse;
		const live = span(
			"00000000-0000-0000-0000-000000000215",
			"2026-09-09T10:13:00Z",
		);
		const idleRequest = { ...request, include_idle: true };
		const historicalBefore = buildActivityDisplayRows(
			before.activity,
			before.mcp_requests ?? [],
			true,
			true,
			"started",
			"desc",
		).filter((row) => row.type === "idle" && !row.current);

		const after = updateMCPRequestActivityCache(before, live, idleRequest);
		const ids = new Set((after.mcp_requests ?? []).map((item) => item.id));
		expect(ids.has("00000000-0000-0000-0000-000000000210")).toBe(false);
		expect(ids.has("00000000-0000-0000-0000-000000000211")).toBe(true);
		expect(ids.has("00000000-0000-0000-0000-000000000212")).toBe(true);
		expect(ids.has("00000000-0000-0000-0000-000000000213")).toBe(true);
		expect(ids.has("00000000-0000-0000-0000-000000000214")).toBe(true);
		expect(ids.has(live.id)).toBe(true);

		const historicalAfter = buildActivityDisplayRows(
			after.activity,
			after.mcp_requests ?? [],
			true,
			true,
			"started",
			"desc",
		).filter((row) => row.type === "idle" && !row.current);
		expect(historicalAfter).toEqual(historicalBefore);
	});

	it("matches realtime rows against ID and exit filters", () => {
		const finished = {
			...running,
			status: "succeeded",
			finished_at: "2026-09-09T12:00:10Z",
			exit_code: 17,
		} satisfies WorkspaceCommandActivity;
		expect(
			commandMatchesRequest(finished, {
				...request,
				id: "00000000-0000",
				exit_code: 17,
			}),
		).toBe(true);
		expect(commandMatchesRequest(finished, { ...request, exit_code: 1 })).toBe(
			false,
		);
	});

	it("matches realtime text filters against input, environment, and output", () => {
		const finished = {
			...running,
			status: "succeeded",
			finished_at: "2026-09-09T12:00:10Z",
			environment: { SAFE_FLAG: "visible" },
			output: "build completed successfully",
		} satisfies WorkspaceCommandActivity;
		expect(
			commandMatchesRequest(finished, {
				...request,
				search: "SAFE_FLAG=visible",
			}),
		).toBe(true);
		expect(
			commandMatchesRequest(finished, {
				...request,
				search: "build completed",
			}),
		).toBe(true);
	});
});
