import { cva } from "class-variance-authority";
import { createContext, type PropsWithChildren, useContext } from "react";
import { Avatar } from "#/components/Avatar/Avatar";
import { cn } from "#/utils/cn";
import type { StepId } from "./steps";

type Variant = "complete" | "current" | "upcoming" | null | undefined;

const VariantContext = createContext<Variant>(null);

type SelectedTemplate = {
	name: string;
	iconUrl?: string;
};

type SelectedModule = {
	id: string;
	name: string;
	iconUrl: string;
};

type SelectionSummaryProps = {
	currentStep: number;
	/**
	 * The highest group number the user has reached. Groups at or below
	 * this value stay clickable and rendered as `complete` even when the
	 * current step is lower, so the sidebar behaves like a browser back
	 * stack. Groups strictly above are `upcoming` and inert.
	 */
	maxReachedStep: number;
	selectedTemplate?: SelectedTemplate;
	selectedModules?: SelectedModule[];
	/**
	 * Jump to a wizard step. `SelectionSummary` calls this from the
	 * numbered step labels and from the selected-template row.
	 */
	onNavigateStep?: (stepId: StepId) => void;
	/**
	 * Jump to a specific module's configuration section. Consumers
	 * should switch to the appropriate step and scroll the module
	 * into view.
	 */
	onNavigateModule?: (moduleId: string) => void;
};

export const SelectionSummary: React.FC<SelectionSummaryProps> = ({
	currentStep,
	maxReachedStep,
	selectedTemplate,
	selectedModules,
	onNavigateStep,
	onNavigateModule,
}) => {
	const variant = (step: number) => {
		if (currentStep === step) return "current";
		if (step <= maxReachedStep) return "complete";
		return "upcoming";
	};
	const reachable = (step: number) => step <= maxReachedStep;
	return (
		<div>
			<h2 className="text-xl font-semibold">Selection</h2>
			<div className="text-sm">
				<VariantContext.Provider value={variant(1)}>
					<StepIndicator
						step={1}
						onClick={
							onNavigateStep && reachable(1)
								? () => onNavigateStep("base-infra")
								: undefined
						}
					>
						Base Template
					</StepIndicator>
					{selectedTemplate ? (
						<BaseTemplateSelection
							template={selectedTemplate}
							onClick={
								onNavigateStep && reachable(1)
									? () => onNavigateStep("base-parameters")
									: undefined
							}
						/>
					) : (
						<StepDivider />
					)}
				</VariantContext.Provider>
				<VariantContext.Provider value={variant(2)}>
					<StepIndicator
						step={2}
						onClick={
							onNavigateStep && reachable(2)
								? () => onNavigateStep("module-select")
								: undefined
						}
					>
						Modules
					</StepIndicator>
					{selectedModules ? (
						<ModuleSelection
							modules={selectedModules}
							onSelectModule={reachable(2) ? onNavigateModule : undefined}
						/>
					) : (
						<StepDivider />
					)}
				</VariantContext.Provider>
				<VariantContext.Provider value={variant(3)}>
					<StepIndicator
						step={3}
						onClick={
							onNavigateStep && reachable(3)
								? () => onNavigateStep("customizations")
								: undefined
						}
					>
						Customizations
					</StepIndicator>
				</VariantContext.Provider>
			</div>
		</div>
	);
};

const stepCircleVariants = cva(
	"rounded-full size-6 border border-solid flex items-center justify-center text-xs",
	{
		variants: {
			variant: {
				complete: "border-border-success bg-surface-green",
				current: "border-border-success",
				upcoming: "border-border text-content-disabled",
			},
		},
	},
);

const stepLabelVariants = cva("font-normal mr-2", {
	variants: {
		variant: {
			complete: "text-content-primary",
			current: "text-content-primary",
			upcoming: "text-content-disabled",
		},
	},
});

type StepIndicatorProps = PropsWithChildren<{
	step: number;
	onClick?: () => void;
}>;

const StepIndicator: React.FC<StepIndicatorProps> = ({
	step,
	onClick,
	children,
}) => {
	const variant = useContext(VariantContext);
	const label = typeof children === "string" ? children : `step ${step}`;

	if (onClick) {
		return (
			<button
				type="button"
				onClick={onClick}
				aria-label={`Go to ${label}`}
				className={cn(
					"flex items-center gap-2 w-full text-left p-0 bg-transparent border-0 cursor-pointer rounded-sm",
					"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-primary",
				)}
			>
				<div className={stepCircleVariants({ variant })}>{step}</div>
				<span className={stepLabelVariants({ variant })}>{children}</span>
			</button>
		);
	}

	return (
		<div className="flex items-center gap-2">
			<div className={stepCircleVariants({ variant })}>{step}</div>
			<span className={stepLabelVariants({ variant })}>{children}</span>
		</div>
	);
};

const stepDividerVariants = cva(
	"border-0 border-l border-solid mx-3 -translate-x-px",
	{
		variants: {
			variant: {
				complete: "border-border-success",
				current: "border-border",
				upcoming: "border-border",
			},
		},
	},
);

type StepDividerProps = PropsWithChildren<{
	className?: string;
}>;

const StepDivider: React.FC<StepDividerProps> = ({ className, children }) => {
	const variant = useContext(VariantContext);

	return (
		<div
			className={cn(
				stepDividerVariants({ variant }),
				children ? "px-3 py-2" : "h-4",
				className,
			)}
		>
			{children}
		</div>
	);
};

type BaseTemplateSelectionProps = {
	template: SelectedTemplate;
	onClick?: () => void;
};

const BaseTemplateSelection: React.FC<BaseTemplateSelectionProps> = ({
	template,
	onClick,
}) => {
	return (
		<StepDivider>
			{onClick ? (
				<button
					type="button"
					onClick={onClick}
					aria-label={`Configure ${template.name}`}
					className={cn(
						"flex items-center gap-2 w-full text-left p-2 rounded-sm bg-transparent border-0 cursor-pointer",
						"text-content-secondary hover:text-content-primary hover:bg-surface-secondary",
						"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-primary",
					)}
				>
					<Avatar src={template.iconUrl} size="sm" variant="icon" />
					<span>{template.name}</span>
				</button>
			) : (
				<div className="flex items-center gap-2 p-2 text-content-secondary">
					<Avatar src={template.iconUrl} size="sm" variant="icon" />
					<span>{template.name}</span>
				</div>
			)}
		</StepDivider>
	);
};

type ModuleSelectionProps = {
	modules: SelectedModule[];
	onSelectModule?: (moduleId: string) => void;
};

const ModuleSelection: React.FC<ModuleSelectionProps> = ({
	modules,
	onSelectModule,
}) => {
	return (
		<StepDivider className="max-h-72 overflow-y-auto">
			{modules.map((module) =>
				onSelectModule ? (
					<button
						key={module.id}
						type="button"
						onClick={() => onSelectModule(module.id)}
						aria-label={`Configure ${module.name}`}
						className={cn(
							"flex items-center gap-2 w-full text-left p-2 rounded-sm bg-transparent border-0 cursor-pointer",
							"text-content-secondary hover:text-content-primary hover:bg-surface-secondary",
							"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-primary",
						)}
					>
						<Avatar src={module.iconUrl} size="sm" variant="icon" />
						<span className="flex-1">{module.name}</span>
					</button>
				) : (
					<div
						key={module.id}
						className="flex items-center gap-2 p-2 text-content-secondary"
					>
						<Avatar src={module.iconUrl} size="sm" variant="icon" />
						<span className="flex-1">{module.name}</span>
					</div>
				),
			)}
		</StepDivider>
	);
};
