import {
	type FC,
	type ReactNode,
	useCallback,
	useReducer,
	useState,
} from "react";

import { useQuery } from "react-query";
import { templateBuilderModules } from "#/api/queries/templateBuilder";
import type {
	TemplateBuilderBasesResponse,
	TemplateBuilderModulesResponse,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import { Link } from "#/components/Link/Link";
import { Margins } from "#/components/Margins/Margins";
import {
	PageHeader,
	PageHeaderSubtitle,
	PageHeaderTitle,
} from "#/components/PageHeader/PageHeader";
import { HasReachedBottomProvider } from "#/hooks/useHasReachedBottom";

import { docs } from "#/utils/docs";
import { BaseInfraSelectStep } from "./BaseInfraSelectStep";
import {
	BaseTemplateParametersStep,
	baseParametersComplete,
} from "./BaseTemplateParametersStep";
import { BuildingTemplateLoader } from "./BuildingTemplateLoader";
import { ModuleSelectStep } from "./ModuleSelectStep";
import {
	ModuleSettingsStep,
	moduleSettingsComplete,
} from "./ModuleSettingsStep";
import { SelectionSummary } from "./SelectionSummary";
import {
	findNextVisibleIndex,
	findPrevVisibleIndex,
	nearestVisible,
	type StepId,
	WIZARD_STEPS,
} from "./steps";
import { TemplateAlternatives } from "./TemplateAlternatives";
import { TemplateCustomizationsStep } from "./TemplateCustomizationsStep";
import {
	initialWizardState,
	type TemplateBuilderWizardState,
	type WizardAction,
	wizardReducer,
} from "./wizardState";

interface TemplateBuilderPageViewProps {
	error: unknown;
	basesData: TemplateBuilderBasesResponse | undefined;
	onCreateTemplate: (state: TemplateBuilderWizardState) => void;
	createError: Error | null;
	isCreating: boolean;
	onClearCreateError?: () => void;
}

export const TemplateBuilderPageView: FC<TemplateBuilderPageViewProps> = ({
	error,
	basesData,
	onCreateTemplate,
	createError,
	isCreating,
	onClearCreateError,
}) => {
	const [state, dispatch] = useReducer(wizardReducer, initialWizardState);
	const [stepIndex, setStepIndex] = useState(0);
	const [maxReachedGroup, setMaxReachedGroup] = useState<1 | 2 | 3>(1);
	const modulesQuery = useQuery(templateBuilderModules(state.selectedBase?.id));

	const moduleVarMap = Object.fromEntries(
		state.modules.map((m) => [m.id, m.variables ?? {}]),
	);

	const currentIndex = nearestVisible(stepIndex, state);
	const currentStep = WIZARD_STEPS[currentIndex];

	const nextIndex = findNextVisibleIndex(currentIndex, state);
	const prevIndex = findPrevVisibleIndex(currentIndex, state);
	const isFirstStep = prevIndex === -1;
	const isLastStep = nextIndex === -1;

	// Keep maxReachedGroup at least as high as the current group; navigating
	// backward via the sidebar must not shrink it.
	if (currentStep.group > maxReachedGroup) {
		setMaxReachedGroup(currentStep.group);
	}

	const canContinue = computeCanContinue(
		currentStep.id,
		state,
		basesData,
		modulesQuery.data,
		moduleVarMap,
	);

	const handleBack = () => {
		if (currentStep.id === "customizations") {
			dispatch({ type: "RESET_CUSTOMIZATIONS" });
			onClearCreateError?.();
		}
		window.scrollTo(0, 0);
		setStepIndex(prevIndex);
	};

	const handleNext = () => {
		if (isLastStep) {
			onCreateTemplate(state);
			return;
		}
		window.scrollTo(0, 0);
		setStepIndex(nextIndex);
	};

	const navigateToStep = (
		targetId: StepId,
		options?: { skipScrollReset?: boolean },
	) => {
		const target = WIZARD_STEPS.findIndex((s) => s.id === targetId);
		if (target < 0) return;
		if (currentStep.id === "customizations" && targetId !== "customizations") {
			dispatch({ type: "RESET_CUSTOMIZATIONS" });
			onClearCreateError?.();
		}
		// The caller sets skipScrollReset when it will manage the scroll
		// position itself, for example by jumping to a specific module
		// section via scrollIntoView.
		if (!options?.skipScrollReset) {
			window.scrollTo(0, 0);
		}
		setStepIndex(nearestVisible(target, state));
	};

	const navigateToModule = (moduleId: string) => {
		const settingsIndex = WIZARD_STEPS.findIndex(
			(s) => s.id === "module-settings",
		);
		// If module-settings is skipped (no configurable vars), fall back to
		// module-select so the user still lands somewhere useful.
		const targetId: StepId =
			settingsIndex >= 0 && !WIZARD_STEPS[settingsIndex].shouldSkip(state)
				? "module-settings"
				: "module-select";
		navigateToStep(targetId, { skipScrollReset: true });
		if (targetId === "module-settings") {
			// Wait for the step render, then scroll the module section into view.
			requestAnimationFrame(() => {
				const el = document.getElementById(`module-config-${moduleId}`);
				el?.scrollIntoView({ behavior: "smooth", block: "start" });
			});
		}
	};

	const handleProvisionerStatusChange = useCallback(
		(value: boolean | undefined) => {
			dispatch({ type: "SET_HAS_PROVISIONERS", value });
		},
		[],
	);

	const handleDeselectModule = (moduleId: string) => {
		// If the only module gets deselected, go back to module selection
		if (state.modules.length === 1) {
			setStepIndex(WIZARD_STEPS.findIndex((s) => s.id === "module-select"));
		}
		dispatch({
			type: "SET_MODULES",
			modules: state.modules.filter((m) => m.id !== moduleId),
			meta: state.selectedModules.filter((m) => m.id !== moduleId),
		});
	};

	if (isCreating) {
		return <BuildingTemplateLoader />;
	}

	return (
		<Margins className="pb-12">
			<PageHeader>
				<PageHeaderTitle>Create new template</PageHeaderTitle>
				<PageHeaderSubtitle>
					A Terraform blueprint for reproducible workspaces.
					<Link
						href={docs("/admin/templates")}
						target="_blank"
						className="ml-1 font-normal"
					>
						View docs
					</Link>
				</PageHeaderSubtitle>
			</PageHeader>

			{error != null && <ErrorAlert error={error} />}

			<div className="flex gap-8">
				{/* Main content area */}
				<div className="flex-1 min-w-0">
					<div className="p-6 border border-solid rounded-lg overflow-x-auto">
						{/*
							 Reset the "has reached bottom" signal on every step change
							 so required fields on a fresh step do not start out flagged.
							*/}
						<HasReachedBottomProvider key={currentStep.id}>
							{renderStepContent(
								currentStep.id,
								state,
								dispatch,
								moduleVarMap,
								createError,
								handleProvisionerStatusChange,
								handleDeselectModule,
							)}
						</HasReachedBottomProvider>
					</div>

					{/* Navigation controls */}
					<div className="flex justify-end mt-6 gap-2">
						{isFirstStep ? (
							<div />
						) : (
							<Button variant="outline" onClick={handleBack}>
								Back
							</Button>
						)}
						<Button onClick={handleNext} disabled={!canContinue}>
							{isLastStep ? "Create Template" : "Continue"}
						</Button>
					</div>

					{currentStep.id === "base-infra" && <TemplateAlternatives />}
				</div>

				{/* Sidebar (top position is 72px so that it can sit below nav) */}
				<div className="w-64 shrink-0 hidden md:block sticky top-[72px] self-start">
					<SelectionSummary
						currentStep={currentStep.group}
						maxReachedStep={maxReachedGroup}
						selectedTemplate={
							state.selectedBase
								? {
										name: state.selectedBase.name,
										iconUrl: state.selectedBase.iconUrl,
									}
								: undefined
						}
						selectedModules={
							state.selectedModules.length > 0
								? state.selectedModules
								: undefined
						}
						onNavigateStep={navigateToStep}
						onNavigateModule={navigateToModule}
					/>
				</div>
			</div>
		</Margins>
	);
};

function renderStepContent(
	stepId: StepId,
	state: TemplateBuilderWizardState,
	dispatch: (action: WizardAction) => void,
	moduleVarMap: Record<string, Record<string, string>>,
	createError: Error | null,
	onProvisionerStatusChange: (value: boolean | undefined) => void,
	onRemoveModule: (moduleId: string) => void,
): ReactNode {
	switch (stepId) {
		case "base-infra":
			return (
				<BaseInfraSelectStep
					selectedBaseId={state.selectedBase?.id ?? null}
					onSelectBase={(base) => dispatch({ type: "SET_BASE", base })}
				/>
			);
		case "base-parameters":
			if (!state.selectedBase) return null;
			return (
				<BaseTemplateParametersStep
					baseId={state.selectedBase.id}
					values={state.baseVariableValues}
					onChangeValues={(values) =>
						dispatch({ type: "SET_BASE_VARIABLES", values })
					}
				/>
			);
		case "module-select":
			if (!state.selectedBase) return null;
			return (
				<ModuleSelectStep
					baseId={state.selectedBase.id}
					selectedModuleIds={state.modules.map((m) => m.id)}
					onChangeModules={(modules, meta) =>
						dispatch({ type: "SET_MODULES", modules, meta })
					}
				/>
			);
		case "module-settings":
			if (!state.selectedBase) return null;
			return (
				<ModuleSettingsStep
					baseId={state.selectedBase.id}
					selectedModuleIds={state.modules.map((m) => m.id)}
					moduleVariables={moduleVarMap}
					onChangeModuleVariables={(moduleId, variables) =>
						dispatch({
							type: "SET_MODULE_VARIABLES",
							moduleId,
							variables,
						})
					}
					onRemoveModule={onRemoveModule}
				/>
			);
		case "customizations":
			return (
				<>
					{createError != null && <ErrorAlert error={createError} />}
					<TemplateCustomizationsStep
						state={state}
						onChangeField={(field, value) =>
							dispatch({
								type: "SET_CUSTOMIZATION",
								field,
								value,
							})
						}
						onProvisionerStatusChange={onProvisionerStatusChange}
					/>
				</>
			);
		default:
			return null;
	}
}

function computeCanContinue(
	stepId: StepId,
	state: TemplateBuilderWizardState,
	basesData: TemplateBuilderBasesResponse | undefined,
	modulesData: TemplateBuilderModulesResponse | undefined,
	moduleVarMap: Record<string, Record<string, string>>,
): boolean {
	switch (stepId) {
		case "base-infra":
			return state.selectedBase !== null;
		case "base-parameters":
			return baseParametersComplete(
				basesData,
				state.selectedBase?.id ?? null,
				state.baseVariableValues,
			);
		case "module-settings":
			return moduleSettingsComplete(
				modulesData,
				state.modules.map((m) => m.id),
				moduleVarMap,
			);
		case "customizations":
			return state.name.trim() !== "" && state.hasProvisioners !== false;
		default:
			return true;
	}
}
