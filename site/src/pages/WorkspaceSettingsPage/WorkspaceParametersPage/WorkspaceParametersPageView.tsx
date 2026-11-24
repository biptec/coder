import { useFormik } from "formik";
import type { FC } from "react";
import * as Yup from "yup";
import type {
	PreviewParameter,
	Workspace,
	WorkspaceBuildParameter,
} from "#/api/typesGenerated";
import { Alert } from "#/components/Alert/Alert";
import { Button } from "#/components/Button/Button";
import { Label } from "#/components/Label/Label";
import { Link } from "#/components/Link/Link";
import { Spinner } from "#/components/Spinner/Spinner";
import { useDebouncedFunction } from "#/hooks/debounce";
import { useSyncFormParameters } from "#/modules/hooks/useSyncFormParameters";
import {
	DynamicParameter,
	getInitialParameterValues,
	useValidationSchemaForDynamicParameters,
} from "#/modules/workspaces/DynamicParameter/DynamicParameter";
import { groupDynamicParameters } from "#/modules/workspaces/DynamicParameter/parameterGroups";
import { RangeParameter } from "#/modules/workspaces/DynamicParameter/RangeParameter";
import { getMonotonicParameterError } from "#/modules/workspaces/DynamicParameter/sliderParameters";
import { cn } from "#/utils/cn";
import { docs } from "#/utils/docs";
import type { AutofillBuildParameter } from "#/utils/richParameters";

type WorkspaceParametersPageViewProps = {
	workspace: Workspace;
	autofillParameters: AutofillBuildParameter[];
	parameters: PreviewParameter[];
	diagnostics: PreviewParameter["diagnostics"];
	canChangeVersions: boolean;
	isSubmitting: boolean;
	submitLabel: string;
	onCancel: () => void;
	onSubmit: (values: {
		rich_parameter_values: WorkspaceBuildParameter[];
	}) => void;
	sendMessage: (formValues: Record<string, string>) => void;
	templateVersionId: string | undefined;
};

export const WorkspaceParametersPageView: FC<
	WorkspaceParametersPageViewProps
> = ({
	workspace,
	autofillParameters,
	parameters,
	diagnostics,
	canChangeVersions,
	isSubmitting,
	submitLabel,
	onSubmit,
	sendMessage,
	onCancel,
	templateVersionId,
}) => {
	// The last persisted build, not a subsequent preview or an unsaved edit,
	// defines the monotonic limit for this form.
	const lastBuildParameters = autofillParameters.filter(
		(parameter) => parameter.source === "active_build",
	);
	const previousValues = new Map(
		lastBuildParameters.map((parameter) => [parameter.name, parameter.value]),
	);
	const hasMonotonicErrors = (values: WorkspaceBuildParameter[]) =>
		values.some(({ name, value }) => {
			const parameter = parameters.find((parameter) => parameter.name === name);
			return (
				parameter &&
				getMonotonicParameterError(parameter, value, previousValues.get(name))
			);
		});
	const form = useFormik({
		onSubmit: (values, helpers) => {
			if (!hasMonotonicErrors(values.rich_parameter_values)) {
				return onSubmit(values);
			}
			helpers.setSubmitting(false);
		},
		initialValues: {
			rich_parameter_values: getInitialParameterValues(
				parameters,
				autofillParameters,
			),
		},
		initialTouched: Object.fromEntries(
			autofillParameters.map((p) => [p.name, true]),
		),
		validationSchema: Yup.object({
			rich_parameter_values: useValidationSchemaForDynamicParameters(
				parameters,
				lastBuildParameters,
			),
		}),
		enableReinitialize: false,
		validateOnChange: true,
		validateOnBlur: true,
	});

	const parameterGroups = groupDynamicParameters(parameters);
	const disabled =
		workspace.outdated &&
		workspace.template_require_active_version &&
		!canChangeVersions;

	// Debounce websocket sends to avoid stale responses overwriting
	// the form while the user is still typing.
	const { debounced: sendDynamicParamsRequest } = useDebouncedFunction(
		(updates: Array<{ parameter: PreviewParameter; value: string }>) => {
			const formInputs: Record<string, string> = {};
			const formParameters = form.values.rich_parameter_values ?? [];
			for (const param of formParameters) {
				if (param?.name && param?.value !== undefined) {
					formInputs[param.name] = param.value;
				}
			}
			for (const { parameter, value } of updates) {
				formInputs[parameter.name] = value;
			}
			sendMessage(formInputs);
		},
		(updates: Array<{ parameter: PreviewParameter; value: string }>) => {
			// Return a debounce for string fields (those that involve typing) and
			// zero debounce for all others (so the UI can react immediately).
			return updates.some(
				({ parameter }) =>
					parameter.form_type === "input" || parameter.form_type === "textarea",
			)
				? 500
				: 0;
		},
	);

	const handleChange = async (
		parameter: PreviewParameter,
		parameterField: string,
		value: string,
	) => {
		await form.setFieldValue(parameterField, {
			name: parameter.name,
			value,
		});
		form.setFieldTouched(parameter.name, true);
		sendDynamicParamsRequest([{ parameter, value }]);
	};

	const handleRangeChange = async (
		minParameter: PreviewParameter,
		maxParameter: PreviewParameter,
		minField: string,
		maxField: string,
		minValue: string,
		maxValue: string,
	) => {
		await Promise.all([
			form.setFieldValue(minField, {
				name: minParameter.name,
				value: minValue,
			}),
			form.setFieldValue(maxField, {
				name: maxParameter.name,
				value: maxValue,
			}),
			form.setFieldTouched(minParameter.name, true),
			form.setFieldTouched(maxParameter.name, true),
		]);
		sendDynamicParamsRequest([
			{ parameter: minParameter, value: minValue },
			{ parameter: maxParameter, value: maxValue },
		]);
	};

	useSyncFormParameters({
		parameters,
		formValues: form.values.rich_parameter_values ?? [],
		touched: form.touched,
		setFieldValue: form.setFieldValue,
	});

	// True when the form holds values the backend hasn't evaluated
	// yet (debounce pending or WS round-trip in flight).
	const hasUnsyncedParameters = (form.values.rich_parameter_values ?? []).some(
		(formParam) => {
			const responseParam = parameters.find((p) => p.name === formParam.name);
			if (!responseParam) {
				return true;
			}
			const responseValue = responseParam.value.valid
				? responseParam.value.value
				: "";
			return formParam.value !== responseValue;
		},
	);

	const hasIncompatibleParameters = parameters.some((parameter) => {
		if (!parameter.mutable && parameter.diagnostics.length > 0) {
			return true;
		}
		return false;
	});

	return (
		<>
			{disabled && (
				<Alert severity="warning" className="mb-8" prominent>
					The template for this workspace requires automatic updates. Update the
					workspace to edit parameters.
				</Alert>
			)}

			{hasIncompatibleParameters && (
				<Alert severity="error" prominent>
					<p className="text-lg leading-normal font-bold m-0">
						Workspace update blocked
					</p>
					<p className="mb-0">
						The new template version includes parameter changes that are
						incompatible with this workspace's existing parameter values. This
						may be caused by:
					</p>
					<ul className="mb-0 pl-4 space-y-1">
						<li>
							New <strong>required</strong> parameters that cannot be provided
							after workspace creation
						</li>
						<li>
							Changes to <strong>valid options or validations</strong> for
							existing parameters
						</li>
						<li>Logic changes that conflict with previously selected values</li>
					</ul>
					<p className="mb-0">
						Please contact the <strong>template administrator</strong> to review
						the changes and ensure compatibility for existing workspaces.
					</p>
					<p className="mb-0">
						Consider supplying defaults for new parameters or validating
						conditional logic against prior workspace states.
					</p>
				</Alert>
			)}

			{diagnostics && diagnostics.length > 0 && (
				<div className="flex flex-col gap-4 mb-8">
					{diagnostics.map((diagnostic, index) => (
						<div
							key={`diagnostic-${diagnostic.summary}-${index}`}
							className={cn(
								"text-xs flex flex-col rounded-md border px-4 pb-3 border-solid",
								diagnostic.severity === "error"
									? "text-content-destructive border-border-destructive"
									: "text-content-warning border-border-warning",
							)}
						>
							<div className="flex items-center m-0">
								<p className="font-medium">{diagnostic.summary}</p>
							</div>
							{diagnostic.detail && (
								<p className="m-0 pb-0">{diagnostic.detail}</p>
							)}
						</div>
					))}
				</div>
			)}

			{(templateVersionId || workspace.latest_build.template_version_id) && (
				<div className="flex flex-col gap-2">
					<Label className="text-sm text-content-secondary">Version ID</Label>
					<p className="m-0 text-xs font-medium font-mono">
						{templateVersionId ?? workspace.latest_build.template_version_id}
					</p>
				</div>
			)}

			<form
				onSubmit={form.handleSubmit}
				className="flex flex-col gap-8"
				data-testid="form"
			>
				{parameters.length > 0 && (
					<section className="flex flex-col gap-9">
						<hgroup>
							<h2 className="text-xl font-medium mb-0">Parameters</h2>
							<p className="text-sm text-content-secondary m-0">
								These are the settings used by your template. Immutable
								parameters cannot be modified once the workspace is created.
								<Link
									href={docs(
										"/admin/templates/extending-templates/dynamic-parameters",
									)}
								>
									View docs
								</Link>
							</p>
						</hgroup>
						{parameterGroups.map((group) => {
							if (group.type === "range") {
								const { minParameter, maxParameter } = group;
								const currentMinIndex =
									form.values.rich_parameter_values?.findIndex(
										(parameter) => parameter.name === minParameter.name,
									) ?? -1;
								const currentMaxIndex =
									form.values.rich_parameter_values?.findIndex(
										(parameter) => parameter.name === maxParameter.name,
									) ?? -1;
								const minFieldIndex =
									currentMinIndex >= 0 ? currentMinIndex : group.minIndex;
								const maxFieldIndex =
									currentMaxIndex >= 0 ? currentMaxIndex : group.maxIndex;
								const minValue =
									form.values.rich_parameter_values?.[minFieldIndex]?.value ??
									"";
								const maxValue =
									form.values.rich_parameter_values?.[maxFieldIndex]?.value ??
									"";
								const isDisabled =
									disabled ||
									minParameter.styling?.disabled ||
									maxParameter.styling?.disabled ||
									!minParameter.mutable ||
									!maxParameter.mutable ||
									isSubmitting;

								return (
									<RangeParameter
										key={group.key}
										minParameter={minParameter}
										maxParameter={maxParameter}
										minValue={minValue}
										maxValue={maxValue}
										onChange={(nextMin, nextMax) =>
											handleRangeChange(
												minParameter,
												maxParameter,
												`rich_parameter_values.${minFieldIndex}`,
												`rich_parameter_values.${maxFieldIndex}`,
												nextMin,
												nextMax,
											)
										}
										disabled={isDisabled}
									/>
								);
							}

							const { parameter, index } = group;
							const currentParameterValueIndex =
								form.values.rich_parameter_values?.findIndex(
									(p) => p.name === parameter.name,
								) ?? -1;
							const parameterFieldIndex =
								currentParameterValueIndex >= 0
									? currentParameterValueIndex
									: index;
							const formValue =
								form.values.rich_parameter_values?.[parameterFieldIndex]
									?.value ?? "";
							const parameterField = `rich_parameter_values.${parameterFieldIndex}`;
							const isDisabled =
								disabled ||
								parameter.styling?.disabled ||
								!parameter.mutable ||
								isSubmitting;

							return (
								<DynamicParameter
									key={group.key}
									parameter={parameter}
									onChange={(value) =>
										handleChange(parameter, parameterField, value)
									}
									autofill={false}
									disabled={isDisabled}
									value={formValue}
									previousValue={previousValues.get(parameter.name)}
								/>
							);
						})}
					</section>
				)}

				<div className="flex justify-end gap-2">
					<Button onClick={onCancel} variant="outline" disabled={isSubmitting}>
						Cancel
					</Button>
					<Button
						type="submit"
						disabled={
							isSubmitting ||
							disabled ||
							hasUnsyncedParameters ||
							!form.isValid ||
							hasMonotonicErrors(form.values.rich_parameter_values) ||
							diagnostics.some(
								(diagnostic) => diagnostic.severity === "error",
							) ||
							parameters.some((parameter) =>
								parameter.diagnostics.some(
									(diagnostic) => diagnostic.severity === "error",
								),
							)
						}
					>
						<Spinner loading={isSubmitting} />
						{submitLabel}
					</Button>
				</div>
			</form>
		</>
	);
};
