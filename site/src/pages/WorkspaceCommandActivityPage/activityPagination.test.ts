import { describe, expect, it } from "vitest";
import {
	getClampedActivityPage,
	parseActivityPage,
	parseActivityPageSize,
} from "./activityPagination";

describe("getClampedActivityPage", () => {
	it("does not reset pagination while the next page uses placeholder data", () => {
		expect(getClampedActivityPage(2, 1, true)).toBeUndefined();
	});

	it("clamps to the last real page after the current page disappears", () => {
		expect(getClampedActivityPage(4, 3, false)).toBe(3);
	});

	it("leaves a valid real page unchanged", () => {
		expect(getClampedActivityPage(2, 3, false)).toBeUndefined();
	});
});

describe("activity pagination inputs", () => {
	it("jumps directly to an arbitrary valid page and clamps out-of-range values", () => {
		expect(parseActivityPage("87", 1, 120)).toBe(87);
		expect(parseActivityPage("999", 2, 120)).toBe(120);
		expect(parseActivityPage("0", 2, 120)).toBe(1);
		expect(parseActivityPage("nope", 7, 120)).toBe(7);
	});

	it("accepts arbitrary row counts within the server limit", () => {
		expect(parseActivityPageSize("137", 50)).toBe(137);
		expect(parseActivityPageSize("0", 50)).toBe(1);
		expect(parseActivityPageSize("999", 50)).toBe(500);
		expect(parseActivityPageSize("2.5", 50)).toBe(50);
	});
});
