import { describe, expect, it } from "vitest";
import {
	defaultWorkspaceActivityPreferences,
	parseWorkspaceActivityPreferences,
} from "./activityPreferences";

describe("workspace activity preferences", () => {
	it("defaults to every ordinary status, all tools, and excludes idle", () => {
		const preferences = defaultWorkspaceActivityPreferences();
		expect(preferences.statuses).toEqual([
			"running",
			"succeeded",
			"failed",
			"interrupted",
		]);
		expect(preferences.statuses).not.toContain("idle");
		expect(preferences.tools).toBeNull();
	});

	it("restores all page filter and table preferences", () => {
		const preferences = parseWorkspaceActivityPreferences({
			version: 1,
			search: "needle",
			statuses: ["failed", "idle"],
			tools: ["process_output", "read_file"],
			source: "mcp",
			startedAfter: "2026-09-09T10:00",
			startedBefore: "2026-09-09T11:00",
			durationMin: "1s",
			durationMax: "2m",
			sortBy: "tool",
			sortDirection: "asc",
			pageSize: 250,
		});
		expect(preferences).toEqual({
			search: "needle",
			statuses: ["failed", "idle"],
			tools: ["process_output", "read_file"],
			source: "mcp",
			startedAfter: "2026-09-09T10:00",
			startedBefore: "2026-09-09T11:00",
			durationMin: "1s",
			durationMax: "2m",
			sortBy: "tool",
			sortDirection: "asc",
			pageSize: 250,
		});
	});

	it("sanitizes malformed stored preferences", () => {
		const preferences = parseWorkspaceActivityPreferences({
			version: 1,
			search: 42,
			statuses: ["failed", "bogus", "failed"],
			tools: [" process_output ", "", "process_output"],
			source: "bogus",
			sortBy: "bogus",
			sortDirection: "sideways",
			pageSize: 999,
		});
		expect(preferences.search).toBe("");
		expect(preferences.statuses).toEqual(["failed"]);
		expect(preferences.tools).toEqual(["process_output"]);
		expect(preferences.source).toBe("all");
		expect(preferences.sortBy).toBe("started");
		expect(preferences.sortDirection).toBe("desc");
		expect(preferences.pageSize).toBe(50);
	});

	it("drops unknown storage versions instead of guessing", () => {
		expect(
			parseWorkspaceActivityPreferences({ version: 2, search: "stale" }),
		).toEqual(defaultWorkspaceActivityPreferences());
	});
});
