import type { PreviewParameter } from "#/api/typesGenerated";
import {
	areSliderBoundsCompatible,
	autoMaxValue,
	clampSliderValue,
	formatSliderValue,
	getMonotonicParameterError,
	getSliderBounds,
} from "./sliderParameters";

const cpuParameter: PreviewParameter = {
	name: "cpu",
	display_name: "CPU",
	description: "",
	type: "number",
	form_type: "slider",
	styling: {
		slider_min: 0.25,
		slider_max: 8,
		step: 0.25,
		display_unit: "vCPU",
	},
	mutable: true,
	default_value: { value: "1.25", valid: true },
	icon: "",
	options: [],
	validations: [],
	required: false,
	order: 0,
	ephemeral: false,
	value: { value: "1.25", valid: true },
	diagnostics: [],
};

describe("sliderParameters", () => {
	it("uses configured slider bounds and step", () => {
		expect(getSliderBounds(cpuParameter)).toEqual({
			min: 0.25,
			max: 8,
			step: 0.25,
			unit: "vCPU",
		});
	});

	it("intersects slider bounds with parameter validations", () => {
		const parameter = {
			...cpuParameter,
			validations: [
				{
					validation_min: 1,
					validation_max: 6,
					validation_error: "",
					validation_regex: null,
					validation_monotonic: null,
				},
			],
		};

		expect(getSliderBounds(parameter)).toEqual({
			min: 1,
			max: 6,
			step: 0.25,
			unit: "vCPU",
		});
	});

	it("intersects increasing and decreasing limits with the previous build", () => {
		const parameter = (direction: "increasing" | "decreasing") => ({
			...cpuParameter,
			validations: [
				{
					validation_min: null,
					validation_max: null,
					validation_error: "",
					validation_regex: null,
					validation_monotonic: direction,
				},
			],
		});
		const increasing = parameter("increasing");
		const decreasing = parameter("decreasing");
		expect(getSliderBounds(increasing, "2").min).toBe(2);
		expect(getSliderBounds(decreasing, "2").max).toBe(2);
		expect(getSliderBounds(increasing, "2.1").min).toBe(2.25);
		expect(getSliderBounds(decreasing, "2.1").max).toBe(2);
		expect(getSliderBounds(increasing, "8").min).toBe(8);
		expect(getSliderBounds(increasing, "9").min).toBeGreaterThan(8);
		expect(getSliderBounds(increasing).min).toBe(0.25);
		expect(getSliderBounds(cpuParameter, "2").min).toBe(0.25);
		expect(getMonotonicParameterError(increasing, "1", "2")).toContain("2");
		expect(getMonotonicParameterError(increasing, "", "2")).toBeDefined();
		expect(getMonotonicParameterError(increasing, "2", "2")).toBeUndefined();
		expect(getMonotonicParameterError(increasing, "3", "2")).toBeUndefined();
		expect(getMonotonicParameterError(decreasing, "3", "2")).toContain("2");
		expect(getMonotonicParameterError(decreasing, "1", "2")).toBeUndefined();
		expect(getMonotonicParameterError(cpuParameter, "1", "2")).toBeUndefined();
	});

	it("compares range bounds with floating point tolerance and default step", () => {
		const defaultStep = getSliderBounds({
			...cpuParameter,
			styling: { slider_min: 0.25, slider_max: 8 },
		});
		const explicitStep = getSliderBounds({
			...cpuParameter,
			styling: { slider_min: 0.25, slider_max: 8, step: 1 },
		});

		expect(areSliderBoundsCompatible(defaultStep, explicitStep)).toBe(true);
		expect(
			areSliderBoundsCompatible(defaultStep, {
				...defaultStep,
				min: 0.2500000001,
			}),
		).toBe(true);
		expect(
			areSliderBoundsCompatible(defaultStep, { ...defaultStep, unit: "GiB" }),
		).toBe(false);
	});

	it("clamps values to slider bounds", () => {
		expect(clampSliderValue(0, { min: 0.25, max: 8 })).toBe(0.25);
		expect(clampSliderValue(10, { min: 0.25, max: 8 })).toBe(8);
	});

	it("calculates automatic maximum using the configured multiplier and step", () => {
		const bounds = getSliderBounds(cpuParameter);
		expect(autoMaxValue(1.5, 2, bounds)).toBe(3);
		expect(autoMaxValue(5, 2, bounds)).toBe(8);
	});

	it("formats decimal values without trailing zeroes", () => {
		expect(formatSliderValue(1.5)).toBe("1.5");
		expect(formatSliderValue(2)).toBe("2");
		expect(formatSliderValue(Number.NaN)).toBe("");
	});
});
