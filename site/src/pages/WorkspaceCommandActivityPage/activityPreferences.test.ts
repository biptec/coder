import { describe, expect, it } from "vitest";
import {
	defaultWorkspaceActivityPreferences,
	parseWorkspaceActivityPreferences,
} from "./activityPreferences";

describe("activity history preferences", () => {
	it("defaults to every real status, all tools and sources, and excludes Idle", () => {
		const preferences = defaultWorkspaceActivityPreferences();
		expect(preferences.statuses).toEqual([
			"running",
			"succeeded",
			"failed",
			"interrupted",
		]);
		expect(preferences.statuses).not.toContain("idle");
		expect(preferences.tools).toBeNull();
		expect(preferences.sources).toEqual([
			"mcp",
			"ssh",
			"reconnecting_pty",
			"chat",
		]);
	});

	it("restores all committed column filters and table preferences", () => {
		const preferences = parseWorkspaceActivityPreferences({
			version: 2,
			id: "9211e11f",
			input: "needle",
			statuses: ["failed", "idle"],
			tools: ["process_output", "read_file"],
			sources: ["mcp", "ssh"],
			startedAfter: "2026-09-09T10:00",
			startedBefore: "2026-09-09T11:00",
			durationMin: "1s",
			durationMax: "2m",
			exitCode: "1",
			sortBy: "tool",
			sortDirection: "asc",
			pageSize: 250,
		});
		expect(preferences).toEqual({
			id: "9211e11f",
			input: "needle",
			statuses: ["failed", "idle"],
			tools: ["process_output", "read_file"],
			sources: ["mcp", "ssh"],
			startedAfter: "2026-09-09T10:00",
			startedBefore: "2026-09-09T11:00",
			durationMin: "1s",
			durationMax: "2m",
			exitCode: "1",
			showInput: true,
			showOutput: true,
			showEnvironment: false,
			showFullContent: false,
			useColors: false,
			sortBy: "tool",
			sortDirection: "asc",
			pageSize: 250,
		});
	});

	it("restores v3 display options and arbitrary page sizes", () => {
		const preferences = parseWorkspaceActivityPreferences({
			version: 3,
			showInput: false,
			showOutput: true,
			showEnvironment: true,
			showFullContent: true,
			useColors: true,
			pageSize: 137,
		});
		expect(preferences.showInput).toBe(false);
		expect(preferences.showOutput).toBe(true);
		expect(preferences.showEnvironment).toBe(true);
		expect(preferences.showFullContent).toBe(true);
		expect(preferences.useColors).toBe(true);
		expect(preferences.pageSize).toBe(137);
	});

	it("migrates the first browser-only preference format", () => {
		const preferences = parseWorkspaceActivityPreferences({
			version: 1,
			search: "old search",
			statuses: ["succeeded", "idle"],
			tools: null,
			source: "mcp",
			startedAfter: "",
			startedBefore: "",
			durationMin: "",
			durationMax: "",
			sortBy: "started",
			sortDirection: "desc",
			pageSize: 50,
		});
		expect(preferences.input).toBe("old search");
		expect(preferences.sources).toEqual(["mcp"]);
		expect(preferences.id).toBe("");
		expect(preferences.exitCode).toBe("");
		expect(preferences.showInput).toBe(true);
		expect(preferences.showOutput).toBe(true);
		expect(preferences.showEnvironment).toBe(false);
	});

	it("sanitizes malformed stored preferences", () => {
		const preferences = parseWorkspaceActivityPreferences({
			version: 2,
			id: 42,
			input: 42,
			statuses: ["failed", "bogus", "failed"],
			tools: [" process_output ", "", "process_output"],
			sources: ["mcp", "vscode", "bogus"],
			sortBy: "bogus",
			sortDirection: "sideways",
			pageSize: 999,
		});
		expect(preferences.id).toBe("");
		expect(preferences.input).toBe("");
		expect(preferences.statuses).toEqual(["failed"]);
		expect(preferences.tools).toEqual(["process_output"]);
		expect(preferences.sources).toEqual(["mcp"]);
		expect(preferences.sortBy).toBe("started");
		expect(preferences.sortDirection).toBe("desc");
		expect(preferences.pageSize).toBe(50);
	});

	it("drops unknown storage versions instead of guessing", () => {
		expect(
			parseWorkspaceActivityPreferences({ version: 99, input: "stale" }),
		).toEqual(defaultWorkspaceActivityPreferences());
	});
});
