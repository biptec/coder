import { type FC, type ReactNode, useId, useRef } from "react";
import { Input } from "#/components/Input/Input";
import { Label } from "#/components/Label/Label";
import { useHasBeenScrolledPast } from "#/hooks/useHasBeenScrolledPast";
import { useHasReachedBottom } from "#/hooks/useHasReachedBottom";
import { cn } from "#/utils/cn";
import type { FormHelpers } from "#/utils/formUtils";

type FormFieldProps = React.ComponentPropsWithRef<"input"> & {
	field: FormHelpers;
	label: ReactNode;
	description?: ReactNode;
	/**
	 * When true and the field is `required` with an empty value, the input
	 * flips to `aria-invalid` (destructive red border) once the user scrolls
	 * past it. The cue clears as soon as they type a value.
	 */
	markInvalidWhenScrolledPastEmpty?: boolean;
};

export const FormField: FC<FormFieldProps> = ({
	field,
	label,
	description,
	className,
	markInvalidWhenScrolledPastEmpty,
	...inputProps
}) => {
	const generatedId = useId();
	const id = inputProps.id ?? generatedId;
	const errorId = `${id}-error`;
	const helperId = `${id}-helper`;
	const descriptionId = `${id}-description`;
	const describedBy = [
		description ? descriptionId : null,
		field.error ? errorId : field.helperText ? helperId : null,
	]
		.filter(Boolean)
		.join(" ");
	const required = inputProps.required ?? false;

	const wrapperRef = useRef<HTMLDivElement>(null);
	const scrolledPast = useHasBeenScrolledPast(wrapperRef);
	const hasReachedBottom = useHasReachedBottom();
	const isEmpty = field.value == null || field.value === "";
	const showRequiredMiss = Boolean(
		markInvalidWhenScrolledPastEmpty &&
			required &&
			isEmpty &&
			(scrolledPast || hasReachedBottom),
	);
	const isInvalid = Boolean(field.error) || showRequiredMiss;

	return (
		<div ref={wrapperRef} className="flex flex-col gap-2">
			<Label htmlFor={id}>
				{label}
				{required && (
					<>
						{" "}
						<span className="text-xs font-bold text-content-destructive">
							*
						</span>
					</>
				)}
			</Label>
			{description && (
				<div id={descriptionId} className="text-xs text-content-secondary">
					{description}
				</div>
			)}
			<Input
				name={field.name}
				value={field.value}
				onChange={field.onChange}
				onBlur={field.onBlur}
				{...inputProps}
				id={id}
				aria-invalid={isInvalid}
				aria-describedby={describedBy || undefined}
				className={cn(isInvalid && "border-border-destructive", className)}
			/>
			{field.error ? (
				<span id={errorId} className="text-xs text-content-destructive">
					{field.helperText}
				</span>
			) : (
				field.helperText && (
					<span id={helperId} className="text-xs text-content-secondary">
						{field.helperText}
					</span>
				)
			)}
		</div>
	);
};
