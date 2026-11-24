import type { Preset } from "#/api/typesGenerated";
import {
	isEditablePrefillPreset,
	mergePresetMutableParameterNames,
} from "./presetUtils";

const preset: Preset = {
	ID: "preset",
	Name: "Resource preset",
	Description: "",
	Parameters: [
		{ Name: "cpu_request_cores", Value: "0.5" },
		{ Name: "cpu_cores", Value: "2" },
	],
	Default: false,
	DesiredPrebuildInstances: null,
	Icon: "",
};

describe("mergePresetMutableParameterNames", () => {
	it("retains known opt-ins when conditional parameters disappear", () => {
		const previous = new Set(["enable_docker", "docker_cpu"]);
		expect(
			mergePresetMutableParameterNames(previous, [
				{ name: "enable_docker", styling: { preset_mutable: true } },
			]),
		).toBe(previous);
	});

	it("does not opt in unknown parameters and respects an explicit opt-out", () => {
		expect(
			mergePresetMutableParameterNames(new Set(["docker_cpu"]), [
				{ name: "docker_cpu", styling: {} },
				{ name: "other", styling: { preset_mutable: true } },
			]),
		).toEqual(new Set(["other"]));
	});
});

describe("isEditablePrefillPreset", () => {
	it("requires every preset parameter to opt in", () => {
		expect(
			isEditablePrefillPreset(
				preset,
				new Set(["cpu_request_cores", "cpu_cores"]),
			),
		).toBe(true);

		expect(
			isEditablePrefillPreset(preset, new Set(["cpu_request_cores"])),
		).toBe(false);
	});

	it("keeps presets locked by default", () => {
		expect(isEditablePrefillPreset(preset, new Set())).toBe(false);
	});

	it("never makes a prebuild preset editable", () => {
		expect(
			isEditablePrefillPreset(
				{ ...preset, DesiredPrebuildInstances: 1 },
				new Set(["cpu_request_cores", "cpu_cores"]),
			),
		).toBe(false);
	});

	it("does not treat an empty preset as an editable prefill", () => {
		expect(
			isEditablePrefillPreset({ ...preset, Parameters: [] }, new Set()),
		).toBe(false);
	});
});
