import { ChevronDownIcon } from "lucide-react";
import {
	type ComponentPropsWithRef,
	type FC,
	useId,
	useMemo,
	useState,
} from "react";
import { Button } from "#/components/Button/Button";
import { Checkbox } from "#/components/Checkbox/Checkbox";
import {
	Command,
	CommandEmpty,
	CommandInput,
	CommandItem,
	CommandList,
} from "#/components/Command/Command";
import { Input } from "#/components/Input/Input";
import {
	Popover,
	PopoverContent,
	PopoverTrigger,
} from "#/components/Popover/Popover";
import { cn } from "#/utils/cn";

export type ActivityFilterOption = {
	value: string;
	label: string;
};

type FilterTriggerProps = ComponentPropsWithRef<"button"> & {
	label: string;
	summary?: string;
	active?: boolean;
};

const FilterTrigger: FC<FilterTriggerProps> = ({
	label,
	summary,
	active,
	className,
	ref,
	...props
}) => (
	<button
		ref={ref}
		type="button"
		{...props}
		className={cn(
			"inline-flex min-w-0 max-w-full items-center gap-1 border-0 bg-transparent p-0 text-xs font-medium text-content-primary hover:text-content-link",
			className,
		)}
		aria-label={`Filter ${label}`}
	>
		{summary && (
			<span
				className={cn(
					"min-w-0 truncate",
					active ? "text-content-primary" : "text-content-secondary",
				)}
			>
				{summary}
			</span>
		)}
		<ChevronDownIcon className="size-3.5 shrink-0" aria-hidden />
	</button>
);

export const MultiSelectColumnFilter: FC<{
	label: string;
	options: readonly ActivityFilterOption[];
	selected: readonly string[] | null;
	nullMeansAll?: boolean;
	searchPlaceholder: string;
	summary: string;
	active?: boolean;
	onApply: (selected: string[] | null) => void;
}> = ({
	label,
	options,
	selected,
	nullMeansAll = false,
	searchPlaceholder,
	summary,
	active,
	onApply,
}) => {
	const allValues = useMemo(
		() => options.map((option) => option.value),
		[options],
	);
	const [open, setOpen] = useState(false);
	const [draft, setDraft] = useState<string[]>([]);

	const openChanged = (nextOpen: boolean) => {
		if (nextOpen) {
			setDraft(selected === null ? [...allValues] : [...selected]);
		}
		// Closing without Apply intentionally discards draft changes.
		setOpen(nextOpen);
	};
	const toggle = (value: string) => {
		setDraft((current) =>
			current.includes(value)
				? current.filter((item) => item !== value)
				: [...current, value],
		);
	};
	const apply = () => {
		const normalized = allValues.filter((value) => draft.includes(value));
		onApply(
			nullMeansAll && normalized.length === allValues.length
				? null
				: normalized,
		);
		setOpen(false);
	};

	return (
		<Popover open={open} onOpenChange={openChanged}>
			<PopoverTrigger asChild>
				<FilterTrigger label={label} summary={summary} active={active} />
			</PopoverTrigger>
			<PopoverContent
				align="start"
				className="w-80 overflow-hidden border-surface-quaternary bg-surface-secondary p-0"
			>
				<Command className="bg-surface-secondary" shouldFilter>
					<CommandInput
						placeholder={searchPlaceholder}
						aria-label={searchPlaceholder}
					/>
					<div className="flex items-center gap-1 border-b border-border px-2 py-1.5">
						<Button
							variant="subtle"
							size="xs"
							className="min-w-0"
							onClick={() => setDraft([...allValues])}
						>
							Select all
						</Button>
						<Button
							variant="subtle"
							size="xs"
							className="min-w-0"
							onClick={() => setDraft([])}
						>
							Clear
						</Button>
					</div>
					<CommandList className="max-h-72">
						<CommandEmpty>No matching {label.toLowerCase()}.</CommandEmpty>
						{options.map((option) => {
							const checked = draft.includes(option.value);
							return (
								<CommandItem
									key={option.value}
									value={`${option.label} ${option.value}`}
									className="rounded-none px-3 font-normal"
									onSelect={() => toggle(option.value)}
								>
									<Checkbox
										checked={checked}
										aria-label={option.label}
										tabIndex={-1}
										className="pointer-events-none"
									/>
									<span className="flex-1 truncate">{option.label}</span>
								</CommandItem>
							);
						})}
					</CommandList>
				</Command>
				<div className="flex items-center justify-end gap-2 border-t border-border p-2">
					<Button variant="subtle" size="sm" onClick={() => setOpen(false)}>
						Cancel
					</Button>
					<Button size="sm" onClick={apply}>
						Apply
					</Button>
				</div>
			</PopoverContent>
		</Popover>
	);
};

export const TextColumnFilter: FC<{
	label: string;
	value: string;
	placeholder: string;
	summary?: string;
	active?: boolean;
	inputType?: React.HTMLInputTypeAttribute;
	onApply: (value: string) => void;
}> = ({
	label,
	value,
	placeholder,
	summary,
	active,
	inputType = "text",
	onApply,
}) => {
	const inputID = useId();
	const [open, setOpen] = useState(false);
	const [draft, setDraft] = useState(value);
	const openChanged = (nextOpen: boolean) => {
		if (nextOpen) setDraft(value);
		setOpen(nextOpen);
	};
	const apply = () => {
		onApply(draft.trim());
		setOpen(false);
	};
	return (
		<Popover open={open} onOpenChange={openChanged}>
			<PopoverTrigger asChild>
				<FilterTrigger label={label} summary={summary} active={active} />
			</PopoverTrigger>
			<PopoverContent
				align="start"
				className="w-80 border-surface-quaternary bg-surface-secondary p-3"
			>
				<label
					htmlFor={inputID}
					className="mb-2 block text-xs font-medium text-content-secondary"
				>
					{label}
				</label>
				<Input
					id={inputID}
					autoFocus
					type={inputType}
					value={draft}
					onChange={(event) => setDraft(event.currentTarget.value)}
					placeholder={placeholder}
					onKeyDown={(event) => {
						if (event.key === "Enter") apply();
					}}
				/>
				<div className="mt-3 flex items-center justify-between gap-2">
					<Button
						variant="subtle"
						size="sm"
						className="min-w-0"
						onClick={() => setDraft("")}
					>
						Clear
					</Button>
					<div className="flex gap-2">
						<Button variant="subtle" size="sm" onClick={() => setOpen(false)}>
							Cancel
						</Button>
						<Button size="sm" onClick={apply}>
							Apply
						</Button>
					</div>
				</div>
			</PopoverContent>
		</Popover>
	);
};

export type InputOutputDisplayOptions = {
	showInput: boolean;
	showOutput: boolean;
	showEnvironment: boolean;
	showFullContent: boolean;
	useColors: boolean;
};

export const InputOutputColumnFilter: FC<{
	value: string;
	placeholder: string;
	summary?: string;
	active?: boolean;
	options: InputOutputDisplayOptions;
	onApply: (value: string, options: InputOutputDisplayOptions) => void;
}> = ({ value, placeholder, summary, active, options, onApply }) => {
	const inputID = useId();
	const optionIDPrefix = useId();
	const [open, setOpen] = useState(false);
	const [draftValue, setDraftValue] = useState(value);
	const [draftOptions, setDraftOptions] = useState(options);
	const openChanged = (nextOpen: boolean) => {
		if (nextOpen) {
			setDraftValue(value);
			setDraftOptions(options);
		}
		setOpen(nextOpen);
	};
	const apply = () => {
		onApply(draftValue.trim(), draftOptions);
		setOpen(false);
	};
	const toggle = (key: keyof InputOutputDisplayOptions) => {
		setDraftOptions((current) => {
			if (
				(key === "showInput" && current.showInput && !current.showOutput) ||
				(key === "showOutput" && current.showOutput && !current.showInput)
			) {
				return current;
			}
			return { ...current, [key]: !current[key] };
		});
	};
	const optionRows: Array<{
		key: keyof InputOutputDisplayOptions;
		label: string;
		disabled?: boolean;
	}> = [
		{ key: "showInput", label: "Input" },
		{ key: "showOutput", label: "Output" },
		{
			key: "showEnvironment",
			label: "Show environment variables",
			disabled: !draftOptions.showInput,
		},
		{ key: "showFullContent", label: "Show full content" },
		{ key: "useColors", label: "Use colors" },
	];

	return (
		<Popover open={open} onOpenChange={openChanged}>
			<PopoverTrigger asChild>
				<FilterTrigger
					label="Input / Output"
					summary={summary}
					active={active}
				/>
			</PopoverTrigger>
			<PopoverContent
				align="start"
				className="w-80 border-surface-quaternary bg-surface-secondary p-3"
			>
				<label
					htmlFor={inputID}
					className="mb-2 block text-xs font-medium text-content-secondary"
				>
					Search input / output
				</label>
				<Input
					id={inputID}
					autoFocus
					value={draftValue}
					onChange={(event) => setDraftValue(event.currentTarget.value)}
					placeholder={placeholder}
					onKeyDown={(event) => {
						if (event.key === "Enter") apply();
					}}
				/>
				<div className="mt-3 space-y-2 border-t border-border pt-3">
					{optionRows.map((option) => (
						<label
							key={option.key}
							htmlFor={`${optionIDPrefix}-${option.key}`}
							className={cn(
								"flex items-center gap-2 text-sm text-content-primary",
								option.disabled && "opacity-50",
							)}
						>
							<Checkbox
								id={`${optionIDPrefix}-${option.key}`}
								checked={draftOptions[option.key]}
								disabled={option.disabled}
								onCheckedChange={() => toggle(option.key)}
							/>
							<span>{option.label}</span>
						</label>
					))}
				</div>
				<div className="mt-3 flex items-center justify-between gap-2">
					<Button
						variant="subtle"
						size="sm"
						className="min-w-0"
						onClick={() => setDraftValue("")}
					>
						Clear search
					</Button>
					<div className="flex gap-2">
						<Button variant="subtle" size="sm" onClick={() => setOpen(false)}>
							Cancel
						</Button>
						<Button size="sm" onClick={apply}>
							Apply
						</Button>
					</div>
				</div>
			</PopoverContent>
		</Popover>
	);
};

export const RangeColumnFilter: FC<{
	label: string;
	firstLabel: string;
	secondLabel: string;
	firstValue: string;
	secondValue: string;
	firstPlaceholder?: string;
	secondPlaceholder?: string;
	inputType?: React.HTMLInputTypeAttribute;
	summary?: string;
	active?: boolean;
	onApply: (first: string, second: string) => void;
}> = ({
	label,
	firstLabel,
	secondLabel,
	firstValue,
	secondValue,
	firstPlaceholder,
	secondPlaceholder,
	inputType = "text",
	summary,
	active,
	onApply,
}) => {
	const firstID = useId();
	const secondID = useId();
	const [open, setOpen] = useState(false);
	const [first, setFirst] = useState(firstValue);
	const [second, setSecond] = useState(secondValue);
	const openChanged = (nextOpen: boolean) => {
		if (nextOpen) {
			setFirst(firstValue);
			setSecond(secondValue);
		}
		setOpen(nextOpen);
	};
	const apply = () => {
		onApply(first.trim(), second.trim());
		setOpen(false);
	};
	return (
		<Popover open={open} onOpenChange={openChanged}>
			<PopoverTrigger asChild>
				<FilterTrigger label={label} summary={summary} active={active} />
			</PopoverTrigger>
			<PopoverContent
				align="start"
				className="w-80 border-surface-quaternary bg-surface-secondary p-3"
			>
				<div className="space-y-3">
					<label
						htmlFor={firstID}
						className="block text-xs font-medium text-content-secondary"
					>
						{firstLabel}
						<Input
							id={firstID}
							className="mt-1"
							autoFocus
							type={inputType}
							value={first}
							onChange={(event) => setFirst(event.currentTarget.value)}
							placeholder={firstPlaceholder}
						/>
					</label>
					<label
						htmlFor={secondID}
						className="block text-xs font-medium text-content-secondary"
					>
						{secondLabel}
						<Input
							id={secondID}
							className="mt-1"
							type={inputType}
							value={second}
							onChange={(event) => setSecond(event.currentTarget.value)}
							placeholder={secondPlaceholder}
						/>
					</label>
				</div>
				<div className="mt-3 flex items-center justify-between gap-2">
					<Button
						variant="subtle"
						size="sm"
						className="min-w-0"
						onClick={() => {
							setFirst("");
							setSecond("");
						}}
					>
						Clear
					</Button>
					<div className="flex gap-2">
						<Button variant="subtle" size="sm" onClick={() => setOpen(false)}>
							Cancel
						</Button>
						<Button size="sm" onClick={apply}>
							Apply
						</Button>
					</div>
				</div>
			</PopoverContent>
		</Popover>
	);
};
