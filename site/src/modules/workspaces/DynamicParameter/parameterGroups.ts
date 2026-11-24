import type { PreviewParameter } from "#/api/typesGenerated";
import { areSliderBoundsCompatible, getSliderBounds } from "./sliderParameters";

export type DynamicParameterGroup =
	| {
			type: "single";
			key: string;
			parameter: PreviewParameter;
			index: number;
	  }
	| {
			type: "range";
			key: string;
			minParameter: PreviewParameter;
			maxParameter: PreviewParameter;
			minIndex: number;
			maxIndex: number;
	  };

interface IndexedParameter {
	parameter: PreviewParameter;
	index: number;
}

interface RangeParameterPair {
	min: IndexedParameter[];
	max: IndexedParameter[];
}

export const groupDynamicParameters = (
	parameters: readonly PreviewParameter[],
): DynamicParameterGroup[] => {
	const ranges = new Map<string, RangeParameterPair>();

	for (const [index, parameter] of parameters.entries()) {
		const group = parameter.styling.range_group;
		const role = parameter.styling.range_role;
		if (!group || (role !== "min" && role !== "max")) {
			continue;
		}

		const pair = ranges.get(group) ?? { min: [], max: [] };
		pair[role].push({ parameter, index });
		ranges.set(group, pair);
	}

	const rangeByMinIndex = new Map<number, DynamicParameterGroup>();
	const consumedIndices = new Set<number>();

	for (const [group, pair] of ranges) {
		if (pair.min.length !== 1 || pair.max.length !== 1) {
			continue;
		}

		const min = pair.min[0];
		const max = pair.max[0];
		if (
			min.parameter.type !== "number" ||
			max.parameter.type !== "number" ||
			min.parameter.form_type !== "slider" ||
			max.parameter.form_type !== "slider" ||
			!areSliderBoundsCompatible(
				getSliderBounds(min.parameter),
				getSliderBounds(max.parameter),
			)
		) {
			continue;
		}

		rangeByMinIndex.set(min.index, {
			type: "range",
			key: `range:${group}`,
			minParameter: min.parameter,
			maxParameter: max.parameter,
			minIndex: min.index,
			maxIndex: max.index,
		});
		consumedIndices.add(max.index);
	}

	const groups: DynamicParameterGroup[] = [];
	for (const [index, parameter] of parameters.entries()) {
		if (consumedIndices.has(index)) {
			continue;
		}

		const range = rangeByMinIndex.get(index);
		if (range) {
			groups.push(range);
			continue;
		}

		groups.push({
			type: "single",
			key: parameter.name,
			parameter,
			index,
		});
	}

	return groups;
};
