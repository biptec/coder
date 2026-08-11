import type { DiffLineAnnotation, FileDiffMetadata } from "@pierre/diffs/react";
import { describe, expect, it } from "vitest";
import { annotationsVersion, codeViewItemVersion } from "./DiffViewer";

const annotation = (
	lineNumber: number,
	side: "additions" | "deletions" = "additions",
): DiffLineAnnotation<string> => ({
	side,
	lineNumber,
	metadata: "active-input",
});

describe("annotationsVersion", () => {
	it("is 0 when there are no annotations", () => {
		expect(annotationsVersion(undefined)).toBe(0);
		expect(annotationsVersion([])).toBe(0);
	});

	it("changes when the line moves but the count stays the same", () => {
		// The regression: a single active comment box moving between lines keeps
		// the count at 1, so a count-based version would not change and CodeView
		// would skip the update.
		expect(annotationsVersion([annotation(5)])).not.toBe(
			annotationsVersion([annotation(10)]),
		);
	});

	it("changes when only the side flips", () => {
		expect(annotationsVersion([annotation(5, "additions")])).not.toBe(
			annotationsVersion([annotation(5, "deletions")]),
		);
	});

	it("is stable for identical annotation content", () => {
		expect(annotationsVersion([annotation(7, "deletions")])).toBe(
			annotationsVersion([annotation(7, "deletions")]),
		);
	});

	it("differs from the empty state for any annotation", () => {
		expect(annotationsVersion([annotation(0)])).not.toBe(0);
	});
});

describe("codeViewItemVersion", () => {
	const fileDiff = (): FileDiffMetadata => ({}) as FileDiffMetadata;

	it("is stable for the same parsed file and annotation state", () => {
		const file = fileDiff();
		expect(codeViewItemVersion(file, undefined)).toBe(
			codeViewItemVersion(file, undefined),
		);
	});

	it("changes when the parsed file object changes", () => {
		// Regression: Git can publish a new diff for the same path while there are
		// no annotations. CodeView skips the replacement when version stays 0.
		expect(codeViewItemVersion(fileDiff(), undefined)).not.toBe(
			codeViewItemVersion(fileDiff(), undefined),
		);
	});

	it("changes when annotations change on the same parsed file", () => {
		const file = fileDiff();
		expect(codeViewItemVersion(file, [annotation(5)])).not.toBe(
			codeViewItemVersion(file, [annotation(6)]),
		);
	});
});
