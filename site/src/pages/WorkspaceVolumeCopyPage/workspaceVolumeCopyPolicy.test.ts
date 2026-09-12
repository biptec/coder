import { describe, expect, it } from "vitest";
import { isWorkspaceVolumeCopyDestinationStatusAllowed } from "./workspaceVolumeCopyPolicy";

describe("isWorkspaceVolumeCopyDestinationStatusAllowed", () => {
	it("allows stopped and running destinations", () => {
		expect(isWorkspaceVolumeCopyDestinationStatusAllowed("stopped")).toBe(true);
		expect(isWorkspaceVolumeCopyDestinationStatusAllowed("running")).toBe(true);
	});

	it.each([
		undefined,
		"canceled",
		"canceling",
		"deleted",
		"deleting",
		"failed",
		"pending",
		"starting",
		"stopping",
	] as const)("blocks transitional or unusable destination status %s", (status) => {
		expect(isWorkspaceVolumeCopyDestinationStatusAllowed(status)).toBe(false);
	});
});
