import type { PreviewParameter } from "#/api/typesGenerated";
import { groupDynamicParameters } from "./parameterGroups";

const makeParameter = (
	overrides: Partial<PreviewParameter> = {},
): PreviewParameter => ({
	name: "parameter",
	display_name: "Parameter",
	description: "",
	type: "string",
	form_type: "input",
	styling: {},
	mutable: true,
	default_value: { value: "", valid: true },
	icon: "",
	options: [],
	validations: [],
	required: false,
	order: 0,
	ephemeral: false,
	value: { value: "", valid: true },
	diagnostics: [],
	...overrides,
});

const rangeParameter = (
	name: string,
	role: "min" | "max",
	styling: Partial<PreviewParameter["styling"]> = {},
): PreviewParameter =>
	makeParameter({
		name,
		type: "number",
		form_type: "slider",
		styling: {
			range_group: "cpu",
			range_role: role,
			...styling,
		},
	});

describe("groupDynamicParameters", () => {
	it("groups the min and max parameters at the first parameter position", () => {
		const minParameter = rangeParameter("cpu_min", "min");
		const maxParameter = rangeParameter("cpu_max", "max");
		const otherParameter = makeParameter({ name: "home_disk" });

		expect(
			groupDynamicParameters([minParameter, otherParameter, maxParameter]),
		).toEqual([
			{
				type: "range",
				key: "range:cpu",
				minParameter,
				maxParameter,
				minIndex: 0,
				maxIndex: 2,
			},
			{
				type: "single",
				key: "home_disk",
				parameter: otherParameter,
				index: 1,
			},
		]);
	});

	it("leaves ranges with incompatible slider bounds as individual parameters", () => {
		const minParameter = rangeParameter("cpu_min", "min", {
			slider_min: 0.25,
			slider_max: 8,
			step: 0.25,
		});
		const maxParameter = rangeParameter("cpu_max", "max", {
			slider_min: 0.5,
			slider_max: 8,
			step: 0.25,
		});

		expect(groupDynamicParameters([minParameter, maxParameter])).toEqual([
			{
				type: "single",
				key: "cpu_min",
				parameter: minParameter,
				index: 0,
			},
			{
				type: "single",
				key: "cpu_max",
				parameter: maxParameter,
				index: 1,
			},
		]);
	});

	it("treats omitted and explicit default steps as compatible", () => {
		const minParameter = rangeParameter("cpu_min", "min", {
			slider_min: 0.25,
			slider_max: 8,
		});
		const maxParameter = rangeParameter("cpu_max", "max", {
			slider_min: 0.25,
			slider_max: 8,
			step: 1,
		});

		expect(groupDynamicParameters([minParameter, maxParameter])).toHaveLength(
			1,
		);
		expect(groupDynamicParameters([minParameter, maxParameter])[0].type).toBe(
			"range",
		);
	});

	it("leaves incomplete range groups as individual parameters", () => {
		const minParameter = rangeParameter("cpu_min", "min");

		expect(groupDynamicParameters([minParameter])).toEqual([
			{
				type: "single",
				key: "cpu_min",
				parameter: minParameter,
				index: 0,
			},
		]);
	});
});
