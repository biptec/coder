import { describe, expect, it } from "vitest";
import type {
	WorkspaceCommandActivity,
	WorkspaceCommandActivityRequest,
	WorkspaceCommandActivityResponse,
} from "#/api/typesGenerated";
import { updateCommandActivityCache } from "./commandActivityCache";

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

	it("updates the existing row when a running command finishes", () => {
		const started = updateCommandActivityCache(response, running, request);
		expect(started).toBeDefined();
		const finished = {
			...running,
			status: "succeeded",
			finished_at: "2026-09-09T12:00:10Z",
			exit_code: 0,
		} satisfies WorkspaceCommandActivity;

		const updated = updateCommandActivityCache(started!, finished, request);
		expect(updated).toBeDefined();
		expect(updated?.activity).toEqual([finished]);
		expect(updated?.total_count).toBe(1);
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

	it("requests a server resync when Idle is enabled", () => {
		const withIdle = {
			...request,
			statuses: [...request.statuses, "idle"],
		} satisfies WorkspaceCommandActivityRequest;

		expect(
			updateCommandActivityCache(response, running, withIdle),
		).toBeUndefined();
	});
});
