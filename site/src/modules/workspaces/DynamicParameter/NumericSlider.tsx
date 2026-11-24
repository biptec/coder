import type { FC } from "react";
import type { PreviewParameter } from "#/api/typesGenerated";
import { Input } from "#/components/Input/Input";
import { Slider } from "#/components/Slider/Slider";
import {
	clampSliderValue,
	formatSliderValue,
	getMonotonicParameterLimit,
	getSliderBounds,
} from "./sliderParameters";

interface NumericSliderProps {
	id: string;
	parameter: PreviewParameter;
	value?: string;
	onChange: (value: string) => void;
	disabled?: boolean;
	previousValue?: string;
}

export const NumericSlider: FC<NumericSliderProps> = ({
	id,
	parameter,
	value,
	onChange,
	disabled,
	previousValue,
}) => {
	const bounds = getSliderBounds(parameter, previousValue);
	const limit = getMonotonicParameterLimit(parameter, previousValue);
	const hasChoices = bounds.min < bounds.max;
	const hasValidBounds = bounds.min <= bounds.max;
	const parsedValue = Number(value);
	const sliderValue = clampSliderValue(
		Number.isFinite(parsedValue) ? parsedValue : bounds.min,
		bounds,
	);

	return (
		<div className="flex flex-col gap-3">
			{hasChoices && (
				<div className="flex items-center gap-3">
					<span className="min-w-fit text-xs text-content-secondary">
						{formatSliderValue(bounds.min)}
					</span>
					<Slider
						id={`${id}-slider`}
						thumbLabels={[parameter.display_name || parameter.name]}
						className="mt-2"
						value={[sliderValue]}
						onValueChange={([nextValue]) => {
							onChange(formatSliderValue(nextValue));
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
			)}
			<div className="flex items-center gap-2">
				<Input
					id={id}
					type="number"
					min={bounds.min}
					max={bounds.max}
					step={bounds.step}
					value={value ?? ""}
					onChange={(event) => onChange(event.currentTarget.value)}
					disabled={disabled || !hasValidBounds}
					required={parameter.required}
				/>
				{bounds.unit && (
					<span className="text-sm text-content-secondary">{bounds.unit}</span>
				)}
			</div>
			{limit && (
				<p className="m-0 text-xs text-content-secondary">
					Value can only{" "}
					{limit.direction === "increasing" ? "increase" : "decrease"} from{" "}
					{formatSliderValue(limit.value)}
					{bounds.unit ? ` ${bounds.unit}` : ""}.
				</p>
			)}
			{!hasValidBounds && (
				<p className="m-0 text-xs text-content-destructive" role="alert">
					No permitted values within the template's bounds.
				</p>
			)}
		</div>
	);
};
