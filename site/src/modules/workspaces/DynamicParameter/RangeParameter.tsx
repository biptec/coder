import { type FC, useEffect, useId, useRef, useState } from "react";
import type { PreviewParameter } from "#/api/typesGenerated";
import { Input } from "#/components/Input/Input";
import { Label } from "#/components/Label/Label";
import { Slider } from "#/components/Slider/Slider";
import { ParameterDiagnostics, ParameterLabel } from "./DynamicParameter";
import {
	autoMaxValue,
	clampSliderValue,
	formatSliderValue,
	getSliderBounds,
} from "./sliderParameters";

export interface RangeParameterProps {
	minParameter: PreviewParameter;
	maxParameter: PreviewParameter;
	minValue: string;
	maxValue: string;
	onChange: (minValue: string, maxValue: string) => void;
	disabled?: boolean;
	isPreset?: boolean;
	autofill?: boolean;
	resetAutoMaxKey?: string;
}

export const RangeParameter: FC<RangeParameterProps> = ({
	minParameter,
	maxParameter,
	minValue,
	maxValue,
	onChange,
	disabled,
	isPreset,
	autofill = false,
	resetAutoMaxKey,
}) => {
	const id = useId();
	const [maxManuallyChanged, setMaxManuallyChanged] = useState(false);
	const resetAutoMaxKeyRef = useRef(resetAutoMaxKey);
	useEffect(() => {
		if (resetAutoMaxKeyRef.current !== resetAutoMaxKey) {
			resetAutoMaxKeyRef.current = resetAutoMaxKey;
			setMaxManuallyChanged(false);
		}
	}, [resetAutoMaxKey]);
	const minInputId = `${id}-min`;
	const maxInputId = `${id}-max`;
	const bounds = getSliderBounds(minParameter);
	const autoMaxMultiplier = minParameter.styling.auto_max_multiplier;
	const parsedMin = minValue.trim() === "" ? Number.NaN : Number(minValue);
	const parsedMax = maxValue.trim() === "" ? Number.NaN : Number(maxValue);
	const hasValidValues =
		Number.isFinite(parsedMin) && Number.isFinite(parsedMax);
	const hasValidOrder = hasValidValues && parsedMin <= parsedMax;
	const safeMin = clampSliderValue(
		Number.isFinite(parsedMin) ? parsedMin : bounds.min,
		bounds,
	);
	const safeMax = clampSliderValue(
		Number.isFinite(parsedMax) ? parsedMax : bounds.max,
		bounds,
	);
	const sliderValues =
		safeMin <= safeMax ? [safeMin, safeMax] : [safeMax, safeMin];
	const labelParameter: PreviewParameter = {
		...minParameter,
		required: minParameter.required || maxParameter.required,
		mutable: minParameter.mutable && maxParameter.mutable,
		ephemeral: minParameter.ephemeral || maxParameter.ephemeral,
		diagnostics: [...minParameter.diagnostics, ...maxParameter.diagnostics],
	};

	const handleMinChange = (rawValue: string) => {
		if (rawValue.trim() === "") {
			onChange(rawValue, maxValue);
			return;
		}

		const parsedValue = Number(rawValue);
		if (
			!Number.isFinite(parsedValue) ||
			parsedValue < bounds.min ||
			parsedValue > bounds.max
		) {
			// Keep intermediate input such as "0" when typing "0.75".
			onChange(rawValue, maxValue);
			return;
		}

		const nextMax =
			autoMaxMultiplier !== undefined &&
			!maxManuallyChanged &&
			Number.isFinite(autoMaxMultiplier)
				? formatSliderValue(
						autoMaxValue(parsedValue, autoMaxMultiplier, bounds),
					)
				: maxValue;
		onChange(rawValue, nextMax);
	};

	const handleMaxChange = (rawValue: string) => {
		setMaxManuallyChanged(true);
		// Validate typed values without silently changing the other field.
		onChange(minValue, rawValue);
	};

	return (
		<div
			className="flex flex-col gap-2"
			data-testid={`parameter-range-${minParameter.styling.range_group}`}
		>
			<ParameterLabel
				id={minInputId}
				parameter={labelParameter}
				isPreset={isPreset}
				autofill={autofill}
			/>
			<div className="flex max-w-lg flex-col gap-4">
				<div className="flex items-center gap-3">
					<span className="min-w-fit text-xs text-content-secondary">
						{formatSliderValue(bounds.min)}
					</span>
					<Slider
						id={`${id}-slider`}
						thumbLabels={[
							`${minParameter.display_name || minParameter.name} Min`,
							`${minParameter.display_name || minParameter.name} Max`,
						]}
						className="mt-2"
						value={sliderValues}
						onValueChange={([nextMin, nextMax]) => {
							if (nextMin === undefined || nextMax === undefined) {
								return;
							}
							const minChanged = nextMin !== safeMin;
							const maxChanged = nextMax !== safeMax;
							if (maxChanged) {
								setMaxManuallyChanged(true);
							}
							const nextAutoMax =
								autoMaxMultiplier !== undefined &&
								!maxManuallyChanged &&
								minChanged &&
								!maxChanged &&
								Number.isFinite(autoMaxMultiplier)
									? autoMaxValue(nextMin, autoMaxMultiplier, bounds)
									: nextMax;
							const finalMin = Math.min(nextMin, nextAutoMax);
							const finalMax = Math.max(nextAutoMax, nextMin);
							onChange(
								formatSliderValue(finalMin),
								formatSliderValue(finalMax),
							);
						}}
						min={bounds.min}
						max={bounds.max}
						step={bounds.step}
						disabled={disabled}
					/>
					<span className="min-w-fit text-xs text-content-secondary">
						{formatSliderValue(bounds.max)}
					</span>
				</div>
				<div className="grid grid-cols-2 gap-4">
					<div className="flex flex-col gap-2">
						<Label
							htmlFor={minInputId}
							className="text-xs text-content-secondary"
						>
							Min
						</Label>
						<div className="flex items-center gap-2">
							<Input
								id={minInputId}
								type="number"
								min={bounds.min}
								max={Number.isFinite(parsedMax) ? parsedMax : bounds.max}
								step={bounds.step}
								value={minValue}
								onChange={(event) => handleMinChange(event.currentTarget.value)}
								disabled={disabled}
								required={labelParameter.required}
							/>
							{bounds.unit && (
								<span className="text-sm text-content-secondary">
									{bounds.unit}
								</span>
							)}
						</div>
					</div>
					<div className="flex flex-col gap-2">
						<Label
							htmlFor={maxInputId}
							className="text-xs text-content-secondary"
						>
							Max
						</Label>
						<div className="flex items-center gap-2">
							<Input
								id={maxInputId}
								type="number"
								min={Number.isFinite(parsedMin) ? parsedMin : bounds.min}
								max={bounds.max}
								step={bounds.step}
								value={maxValue}
								onChange={(event) => handleMaxChange(event.currentTarget.value)}
								disabled={disabled}
								required={labelParameter.required}
							/>
							{bounds.unit && (
								<span className="text-sm text-content-secondary">
									{bounds.unit}
								</span>
							)}
						</div>
					</div>
				</div>
				{hasValidValues && !hasValidOrder && (
					<p className="m-0 text-xs text-content-destructive" role="alert">
						Min must be less than or equal to Max.
					</p>
				)}
			</div>
			<ParameterDiagnostics diagnostics={labelParameter.diagnostics} />
		</div>
	);
};
