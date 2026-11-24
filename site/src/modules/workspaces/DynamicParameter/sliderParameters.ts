import type { PreviewParameter } from "#/api/typesGenerated";

export interface SliderBounds {
	min: number;
	max: number;
	step: number;
	unit?: string;
}

export const getMonotonicParameterLimit = (
	parameter: PreviewParameter,
	previousValue?: string,
) => {
	if (parameter.type !== "number" || !previousValue?.trim()) {
		return undefined;
	}
	const value = Number(previousValue);
	if (!Number.isFinite(value)) {
		return undefined;
	}
	const direction = parameter.validations.find(
		(validation) =>
			validation.validation_monotonic === "increasing" ||
			validation.validation_monotonic === "decreasing",
	)?.validation_monotonic;
	if (direction !== "increasing" && direction !== "decreasing") {
		return undefined;
	}
	return { direction, value };
};

export const getMonotonicParameterError = (
	parameter: PreviewParameter,
	value?: string,
	previousValue?: string,
): string | undefined => {
	const limit = getMonotonicParameterLimit(parameter, previousValue);
	if (!limit) {
		return undefined;
	}
	if (!value?.trim() || !Number.isFinite(Number(value))) {
		return "Enter a valid number.";
	}
	if (limit.direction === "increasing" && Number(value) < limit.value) {
		return `Value must only ever increase (last value was ${previousValue}).`;
	}
	if (limit.direction === "decreasing" && Number(value) > limit.value) {
		return `Value must only ever decrease (last value was ${previousValue}).`;
	}
	return undefined;
};

export const getSliderBounds = (
	parameter: PreviewParameter,
	previousValue?: string,
): SliderBounds => {
	const validationMins = parameter.validations
		.map((validation) => validation.validation_min)
		.filter((value): value is number => value !== null);
	const validationMaxes = parameter.validations
		.map((validation) => validation.validation_max)
		.filter((value): value is number => value !== null);

	const minCandidates = [
		parameter.styling.slider_min,
		validationMins.length > 0 ? Math.max(...validationMins) : undefined,
	].filter((value): value is number => value !== undefined && value !== null);
	const maxCandidates = [
		parameter.styling.slider_max,
		validationMaxes.length > 0 ? Math.min(...validationMaxes) : undefined,
	].filter((value): value is number => value !== undefined && value !== null);
	const configuredMin =
		minCandidates.length > 0 ? Math.max(...minCandidates) : 0;
	const configuredMax =
		maxCandidates.length > 0 ? Math.min(...maxCandidates) : 100;
	const hasValidBounds =
		Number.isFinite(configuredMin) &&
		Number.isFinite(configuredMax) &&
		configuredMin < configuredMax;
	const configuredStep = parameter.styling.step ?? 1;

	const min = hasValidBounds ? configuredMin : 0;
	const max = hasValidBounds ? configuredMax : 100;
	const step =
		Number.isFinite(configuredStep) && configuredStep > 0 ? configuredStep : 1;
	const limit = getMonotonicParameterLimit(parameter, previousValue);
	// Keep the template's step grid when intersecting it with the last build.
	const lower =
		limit?.direction === "increasing" && limit.value > min
			? Math.max(
					limit.value,
					Number(
						(min + Math.ceil((limit.value - min) / step - 1e-8) * step).toFixed(
							6,
						),
					),
				)
			: min;
	const upper =
		limit?.direction === "decreasing" && limit.value < max
			? Math.min(
					limit.value,
					Number(
						(
							min +
							Math.floor((limit.value - min) / step + 1e-8) * step
						).toFixed(6),
					),
				)
			: max;

	return {
		min: lower,
		max: upper,
		step,
		unit: parameter.styling.display_unit || undefined,
	};
};

export const areSliderBoundsCompatible = (
	left: SliderBounds,
	right: SliderBounds,
): boolean => {
	const equal = (a: number, b: number) =>
		Math.abs(a - b) <= 1e-8 * Math.max(1, Math.abs(a), Math.abs(b));

	return (
		equal(left.min, right.min) &&
		equal(left.max, right.max) &&
		equal(left.step, right.step) &&
		left.unit === right.unit
	);
};

export const clampSliderValue = (
	value: number,
	bounds: Pick<SliderBounds, "min" | "max">,
): number => Math.min(bounds.max, Math.max(bounds.min, value));

export const autoMaxValue = (
	minValue: number,
	multiplier: number,
	bounds: SliderBounds,
): number => {
	const requestedMax = Math.max(minValue, minValue * multiplier);
	const clampedMax = clampSliderValue(requestedMax, bounds);
	const steps = Math.ceil((clampedMax - bounds.min) / bounds.step - 1e-8);
	return clampSliderValue(bounds.min + steps * bounds.step, bounds);
};

export const formatSliderValue = (value: number): string => {
	if (!Number.isFinite(value)) {
		return "";
	}

	return String(Number(value.toFixed(6)));
};
