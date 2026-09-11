import { describe, expect, it } from "vitest";
import { getClampedActivityPage } from "./activityPagination";

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
