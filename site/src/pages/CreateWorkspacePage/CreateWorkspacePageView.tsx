import { type FormikContextType, useFormik } from "formik";
import { ArrowLeftIcon, ExternalLinkIcon } from "lucide-react";
import {
	type FC,
	useCallback,
	useEffect,
	useId,
	useMemo,
	useRef,
	useState,
} from "react";
import { Link as RouterLink } from "react-router";
import * as Yup from "yup";
import type * as TypesGen from "#/api/typesGenerated";
import type {
	FriendlyDiagnostic,
	PreviewParameter,
} from "#/api/typesGenerated";
import { Alert } from "#/components/Alert/Alert";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Avatar } from "#/components/Avatar/Avatar";
import { Badge } from "#/components/Badge/Badge";
import { Button } from "#/components/Button/Button";
import {
	Combobox,
	ComboboxButton,
	ComboboxContent,
	ComboboxItem,
	ComboboxTrigger,
} from "#/components/Combobox/Combobox";
import { ExternalImage } from "#/components/ExternalImage/ExternalImage";
import {
	HelpPopover,
	HelpPopoverContent,
	HelpPopoverIconTrigger,
} from "#/components/HelpPopover/HelpPopover";
import { Input } from "#/components/Input/Input";
import { Label } from "#/components/Label/Label";
import { Link } from "#/components/Link/Link";
import { Spinner } from "#/components/Spinner/Spinner";
import { Switch } from "#/components/Switch/Switch";
import { WorkspaceUserAutocomplete } from "#/components/UserAutocomplete/UserAutocomplete";
import { useDebouncedFunction } from "#/hooks/debounce";
import type { ExternalAuthPollingState } from "#/hooks/useExternalAuth";
import { useSyncFormParameters } from "#/modules/hooks/useSyncFormParameters";
import {
	Diagnostics,
	DynamicParameter,
	getInitialParameterValues,
	useValidationSchemaForDynamicParameters,
} from "#/modules/workspaces/DynamicParameter/DynamicParameter";
import { groupDynamicParameters } from "#/modules/workspaces/DynamicParameter/parameterGroups";
import { RangeParameter } from "#/modules/workspaces/DynamicParameter/RangeParameter";
import { generateWorkspaceName } from "#/modules/workspaces/generateWorkspaceName";
import { docs } from "#/utils/docs";
import { nameValidator } from "#/utils/formUtils";
import type { AutofillBuildParameter } from "#/utils/richParameters";
import type { CreateWorkspaceMode } from "./CreateWorkspacePage";
import { ExternalAuthButton } from "./ExternalAuthButton";
import type { CreateWorkspacePermissions } from "./permissions";
import { isEditablePrefillPreset } from "./presetUtils";

interface CreateWorkspacePageViewProps {
	autofillParameters: AutofillBuildParameter[];
	canUpdateTemplate?: boolean;
	creatingWorkspace: boolean;
	defaultName?: string | null;
	defaultOwner: TypesGen.MinimalUser;
	diagnostics: readonly FriendlyDiagnostic[];
	disabledParams?: string[];
	error: unknown;
	externalAuth: TypesGen.TemplateVersionExternalAuth[];
	externalAuthPollingState: Record<string, ExternalAuthPollingState>;
	hasAllRequiredExternalAuth: boolean;
	hasIgnoredUrlParams?: boolean;
	mode: CreateWorkspaceMode;
	parameters: PreviewParameter[];
	presetMutableParameterNames: ReadonlySet<string>;
	permissions: CreateWorkspacePermissions;
	presets: TypesGen.Preset[];
	template: TypesGen.Template;
	urlPreset?: TypesGen.Preset;
	urlPresetError?: string;
	versionId?: string;
	versionName?: string;
	onCancel: () => void;
	onSubmit: (
		req: TypesGen.CreateWorkspaceRequest,
		owner: TypesGen.MinimalUser,
	) => void;
	resetMutation: () => void;
	sendMessage: (message: Record<string, string>, ownerId?: string) => void;
	startPollingExternalAuth: (providerId: string) => void;
	owner: TypesGen.MinimalUser;
	setOwner: (user: TypesGen.MinimalUser) => void;
}

export const CreateWorkspacePageView: FC<CreateWorkspacePageViewProps> = ({
	autofillParameters,
	canUpdateTemplate,
	creatingWorkspace,
	defaultName,
	defaultOwner,
	diagnostics,
	disabledParams,
	error,
	externalAuth,
	externalAuthPollingState,
	hasAllRequiredExternalAuth,
	hasIgnoredUrlParams,
	mode,
	parameters,
	presetMutableParameterNames,
	permissions,
	presets = [],
	template,
	urlPreset,
	urlPresetError,
	versionId,
	versionName,
	onSubmit,
	onCancel,
	resetMutation,
	sendMessage,
	startPollingExternalAuth,
	owner,
	setOwner,
}) => {
	const [suggestedName, setSuggestedName] = useState(generateWorkspaceName);
	const [showPresetParameters, setShowPresetParameters] = useState(false);
	const id = useId();
	const workspaceNameInputRef = useRef<HTMLInputElement>(null);
	const rerollSuggestedName = useCallback(() => {
		setSuggestedName(() => generateWorkspaceName());
	}, []);

	const autofillByName = Object.fromEntries(
		autofillParameters.map((param) => [param.name, param]),
	);
	const parameterGroups = useMemo(
		() => groupDynamicParameters(parameters),
		[parameters],
	);
	const editablePrefillPresets = useMemo(
		() =>
			presets.filter((preset) =>
				isEditablePrefillPreset(preset, presetMutableParameterNames),
			),
		[presets, presetMutableParameterNames],
	);
	const editablePrefillPresetIDs = useMemo(
		() => new Set(editablePrefillPresets.map((preset) => preset.ID)),
		[editablePrefillPresets],
	);
	const lockedPresets = useMemo(
		() => presets.filter((preset) => !editablePrefillPresetIDs.has(preset.ID)),
		[presets, editablePrefillPresetIDs],
	);

	// Only touched fields are sent to the websocket
	// Autofilled parameters are marked as touched since they have been modified
	const initialTouched = Object.fromEntries(
		parameters.filter((p) => autofillByName[p.name]).map((p) => [p.name, true]),
	);
	if (defaultName) {
		initialTouched.name = true;
	}

	// The form parameters values hold the working state of the parameters that will be submitted when creating a workspace
	// 1. The form parameter values are initialized from the websocket response when the form is mounted
	// 2. Only touched form fields are sent to the websocket, a field is touched if edited by the user or set by autofill
	// 3. The websocket response may add or remove parameters, these are added or removed from the form values in the useSyncFormParameters hook
	// 4. All existing form parameters are updated to match the websocket response
	//    in the useSyncFormParameters hook, unless they have been touched by the
	//    user or auto-filled.
	const form: FormikContextType<TypesGen.CreateWorkspaceRequest> =
		useFormik<TypesGen.CreateWorkspaceRequest>({
			initialValues: {
				name: defaultName ?? "",
				template_id: template.id,
				rich_parameter_values: getInitialParameterValues(
					parameters,
					autofillParameters,
				),
			},
			initialTouched,
			validationSchema: Yup.object({
				name: nameValidator("Workspace Name"),
				rich_parameter_values:
					useValidationSchemaForDynamicParameters(parameters),
			}),
			enableReinitialize: false,
			validateOnChange: true,
			validateOnBlur: true,
			onSubmit: (request) => {
				if (!hasAllRequiredExternalAuth) {
					return;
				}

				onSubmit(request, owner);
			},
		});

	useEffect(() => {
		if (error) {
			window.scrollTo(0, 0);
		}
	}, [error]);

	useEffect(() => {
		if (form.submitCount > 0 && Object.keys(form.errors).length > 0) {
			workspaceNameInputRef.current?.scrollIntoView({
				behavior: "smooth",
				block: "center",
			});
			workspaceNameInputRef.current?.focus();
		}
	}, [form.submitCount, form.errors]);

	const presetOptions = useMemo(
		() => [
			{ label: "None", value: "undefined", icon: "", description: "" },
			...lockedPresets.map((preset) => ({
				label: preset.Default ? `${preset.Name} (Default)` : preset.Name,
				value: preset.ID,
				icon: preset.Icon,
				description: preset.Description,
			})),
		],
		[lockedPresets],
	);
	const [selectedPresetIndex, setSelectedPresetIndex] = useState(0);
	const [selectedPrefillPresetId, setSelectedPrefillPresetId] = useState<
		string | undefined
	>();
	const [prefillRevision, setPrefillRevision] = useState(0);
	const appliedPrefillPresetKey = useRef<string | undefined>(undefined);

	useEffect(() => {
		// URL preset takes precedence over the template default.
		const initialPreset = urlPreset ?? presets.find((preset) => preset.Default);
		if (initialPreset && editablePrefillPresetIDs.has(initialPreset.ID)) {
			setSelectedPresetIndex(0);
			setSelectedPrefillPresetId(initialPreset.ID);
			form.setFieldValue("template_version_preset_id", undefined);
			return;
		}

		const lockedPresetIndex = lockedPresets.findIndex(
			(preset) => preset.ID === initialPreset?.ID,
		);
		if (initialPreset && lockedPresetIndex >= 0) {
			setSelectedPresetIndex(lockedPresetIndex + 1);
			setSelectedPrefillPresetId(undefined);
			form.setFieldValue("template_version_preset_id", initialPreset.ID);
			return;
		}

		setSelectedPresetIndex(0);
		setSelectedPrefillPresetId(undefined);
		form.setFieldValue("template_version_preset_id", undefined);
	}, [
		presets,
		lockedPresets,
		editablePrefillPresetIDs,
		form.setFieldValue,
		urlPreset,
	]);

	const [presetParameterNames, setPresetParameterNames] = useState<string[]>(
		[],
	);

	// include any modified parameters and all touched parameters to the websocket request
	const { debounced: sendDynamicParamsRequest } = useDebouncedFunction(
		(
			parameters: Array<{
				name: string;
				value: string;
				formType?: PreviewParameter["form_type"];
			}>,
			ownerId?: string,
		) => {
			const formInputs: Record<string, string> = {};
			const formParameters = form.values.rich_parameter_values ?? [];

			for (const { name, value } of parameters) {
				formInputs[name] = value;
			}

			for (const [fieldName, isTouched] of Object.entries(form.touched)) {
				if (isTouched && !parameters.some((p) => p.name === fieldName)) {
					const param = formParameters.find((p) => p.name === fieldName);
					if (param?.value) {
						formInputs[fieldName] = param.value;
					}
				}
			}

			sendMessage(formInputs, ownerId);
		},
		(
			parameters: Array<{
				name: string;
				value: string;
				formType?: PreviewParameter["form_type"];
			}>,
			_ownerId?: string,
		) => {
			// Return a debounce for string fields (those that involve typing) and
			// zero debounce for all others (so the UI can react immediately).
			return parameters.some(
				({ formType }) => formType === "input" || formType === "textarea",
			)
				? 500
				: 0;
		},
	);

	useEffect(() => {
		const selectedBuildPreset = presets.find(
			(preset) => preset.ID === presetOptions[selectedPresetIndex]?.value,
		);
		const selectedPrefillPreset = presets.find(
			(preset) => preset.ID === selectedPrefillPresetId,
		);
		const selectedPreset = selectedPrefillPreset ?? selectedBuildPreset;
		const isEditablePrefill = selectedPrefillPreset !== undefined;

		if (!selectedPreset?.Parameters) {
			setPresetParameterNames([]);
			appliedPrefillPresetKey.current = undefined;
			return;
		}

		if (isEditablePrefill) {
			setPresetParameterNames([]);
			const presetKey = `${selectedPreset.ID}:${prefillRevision}`;
			if (appliedPrefillPresetKey.current === presetKey) {
				return;
			}
			appliedPrefillPresetKey.current = presetKey;
		} else {
			appliedPrefillPresetKey.current = undefined;
			setPresetParameterNames(selectedPreset.Parameters.map((p) => p.Name));
		}

		const currentValues = form.values.rich_parameter_values ?? [];
		const updates: Array<{
			field: string;
			fieldValue: TypesGen.WorkspaceBuildParameter;
			parameter: PreviewParameter;
			presetValue: string;
		}> = [];

		for (const presetParameter of selectedPreset.Parameters) {
			const parameterIndex = parameters.findIndex(
				(parameter) => parameter.name === presetParameter.Name,
			);
			if (parameterIndex === -1) continue;

			const parameter = parameters[parameterIndex];
			const currentValue = currentValues.find(
				(value) => value.name === presetParameter.Name,
			)?.value;

			if (currentValue !== presetParameter.Value) {
				updates.push({
					field: `rich_parameter_values.${parameterIndex}`,
					fieldValue: {
						name: presetParameter.Name,
						value: presetParameter.Value,
					},
					parameter,
					presetValue: presetParameter.Value,
				});
			}
		}

		for (const update of updates) {
			form.setFieldValue(update.field, update.fieldValue);
			form.setFieldTouched(update.parameter.name, true);
		}

		if (isEditablePrefill) {
			// A preset can enable conditional parameters that are not in the
			// current preview. Send their values together with the enabling
			// parameter so the next preview resolves the complete preset.
			for (const parameter of selectedPreset.Parameters) {
				form.setFieldTouched(parameter.Name, true);
			}
			sendDynamicParamsRequest(
				selectedPreset.Parameters.map(({ Name, Value }) => ({
					name: Name,
					value: Value,
				})),
			);
		} else if (updates.length > 0) {
			sendDynamicParamsRequest(
				updates.map((update) => ({
					name: update.parameter.name,
					value: update.presetValue,
					formType: update.parameter.form_type,
				})),
			);
		}
	}, [
		presetOptions,
		selectedPresetIndex,
		selectedPrefillPresetId,
		prefillRevision,
		presets,
		form.setFieldValue,
		form.setFieldTouched,
		parameters,
		form.values.rich_parameter_values,
		sendDynamicParamsRequest,
	]);

	const handleOwnerChange = (user: TypesGen.MinimalUser) => {
		setOwner(user);
		sendDynamicParamsRequest([], user.id);
	};

	const handleChange = async (
		parameter: PreviewParameter,
		parameterField: string,
		value: string,
	) => {
		const currentFormValue = form.values.rich_parameter_values?.find(
			(p) => p.name === parameter.name,
		)?.value;

		await form.setFieldValue(parameterField, {
			name: parameter.name,
			value,
		});

		// Only send the request if the value has changed from the form value
		if (currentFormValue !== value) {
			form.setFieldTouched(parameter.name, true);
			sendDynamicParamsRequest([
				{ name: parameter.name, value, formType: parameter.form_type },
			]);
		}
	};

	const handleRangeChange = async (
		minParameter: PreviewParameter,
		maxParameter: PreviewParameter,
		minField: string,
		maxField: string,
		minValue: string,
		maxValue: string,
	) => {
		const currentValues = form.values.rich_parameter_values ?? [];
		const currentMinValue = currentValues.find(
			(parameter) => parameter.name === minParameter.name,
		)?.value;
		const currentMaxValue = currentValues.find(
			(parameter) => parameter.name === maxParameter.name,
		)?.value;

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

		if (currentMinValue !== minValue || currentMaxValue !== maxValue) {
			sendDynamicParamsRequest([
				{ name: minParameter.name, value: minValue },
				{ name: maxParameter.name, value: maxValue },
			]);
		}
	};

	useSyncFormParameters({
		parameters,
		formValues: form.values.rich_parameter_values ?? [],
		touched: form.touched,
		setFieldValue: form.setFieldValue,
	});

	const disabled =
		creatingWorkspace ||
		!hasAllRequiredExternalAuth ||
		diagnostics.some((diagnostic) => diagnostic.severity === "error") ||
		parameters.some((parameter) =>
			parameter.diagnostics.some(
				(diagnostic) => diagnostic.severity === "error",
			),
		);
	const parameterFieldsDisabled =
		creatingWorkspace ||
		!hasAllRequiredExternalAuth ||
		diagnostics.some((diagnostic) => diagnostic.severity === "error") ||
		parameters.some((parameter) =>
			parameter.diagnostics.some(
				(diagnostic) =>
					diagnostic.severity === "error" &&
					diagnostic.extra?.code !== "range_validation",
			),
		);

	return (
		<>
			<div className="sticky top-5 ml-10">
				<button
					onClick={onCancel}
					type="button"
					className="flex items-center gap-2 bg-transparent border-none text-content-secondary hover:text-content-primary translate-y-[68px]"
				>
					<ArrowLeftIcon size={20} />
					Go back
				</button>
			</div>
			<div className="flex flex-col gap-6 max-w-screen-md mx-auto pb-96">
				<header className="flex flex-col items-start gap-3 mt-10">
					<div className="flex items-center gap-2 justify-between w-full">
						<span className="flex items-center gap-2">
							<Avatar
								variant="icon"
								size="md"
								src={template.icon}
								fallback={template.name}
							/>
							<p className="text-base font-medium m-0">
								{template.display_name.length > 0
									? template.display_name
									: template.name}
							</p>
							{template.deprecated && (
								<Badge variant="warning" size="sm">
									Deprecated
								</Badge>
							)}
						</span>
						{canUpdateTemplate && versionName && (
							<Button asChild size="sm" variant="outline">
								<RouterLink
									to={`/templates/${template.organization_name}/${template.name}/versions/${versionName}/edit`}
								>
									<ExternalLinkIcon />
									View source
								</RouterLink>
							</Button>
						)}
					</div>
					<span className="flex flex-row items-center gap-2">
						<h1 className="text-3xl font-semibold m-0">New workspace</h1>

						<HelpPopover>
							<HelpPopoverIconTrigger />
							<HelpPopoverContent className="max-w-xs text-sm">
								Dynamic Parameters enhances Coder's existing parameter system
								with real-time validation, conditional parameter behavior, and
								richer input types.
								<br />
								<Link
									href={docs(
										"/admin/templates/extending-templates/dynamic-parameters",
									)}
								>
									View docs
								</Link>
							</HelpPopoverContent>
						</HelpPopover>
					</span>
				</header>

				<form
					onSubmit={form.handleSubmit}
					aria-label="Create workspace form"
					className="flex flex-col gap-10 w-full border border-border-default border-solid rounded-lg p-6"
					data-testid="form"
				>
					{Boolean(error) && <ErrorAlert error={error} />}

					{urlPresetError && (
						<Alert severity="warning" dismissible>
							{urlPresetError}
						</Alert>
					)}

					{hasIgnoredUrlParams && urlPreset && (
						<Alert severity="info" dismissible>
							Preset selected. <code>param.*</code> URL parameters have been
							ignored. Use either <code>preset</code> or <code>param.*</code>,
							not both.
						</Alert>
					)}

					{mode === "duplicate" && (
						<Alert
							severity="info"
							dismissible
							data-testid="duplication-warning"
						>
							Duplicating a workspace only copies its parameters. No state from
							the old workspace is copied over.
						</Alert>
					)}

					<section className="flex flex-col gap-4">
						<hgroup>
							<h2 className="text-xl font-semibold m-0">General</h2>
							<p className="text-sm text-content-secondary mt-0">
								{permissions.createWorkspaceForAny
									? "Only admins can create workspaces for other users."
									: "The name of your new workspace."}
							</p>
						</hgroup>
						<div>
							{versionId && versionId !== template.active_version_id && (
								<div className="flex flex-col gap-2 pb-4">
									<Label className="text-sm" htmlFor={`${id}-version-id`}>
										Version ID
									</Label>
									<Input id={`${id}-version-id`} value={versionId} disabled />
									<span className="text-xs text-content-secondary">
										This parameter has been preset, and cannot be modified.
									</span>
								</div>
							)}
							<div className="flex gap-4 flex-wrap">
								<div className="flex flex-col gap-2 flex-1">
									<Label className="text-sm" htmlFor={`${id}-workspace-name`}>
										Workspace name
									</Label>
									<div className="flex flex-col">
										<Input
											id={`${id}-workspace-name`}
											ref={workspaceNameInputRef}
											value={form.values.name}
											onChange={(e) => {
												form.setFieldValue("name", e.target.value.trim());
												resetMutation();
											}}
											disabled={creatingWorkspace}
										/>
										{form.touched.name && form.errors.name && (
											<div className="text-content-destructive text-xs mt-2">
												{form.errors.name}
											</div>
										)}
										<div className="flex gap-2 text-xs text-content-secondary items-center">
											Need a suggestion?
											<Button
												variant="subtle"
												size="sm"
												onClick={async () => {
													await form.setFieldValue("name", suggestedName);
													rerollSuggestedName();
												}}
											>
												{suggestedName}
											</Button>
										</div>
									</div>
								</div>
								{permissions.createWorkspaceForAny && (
									<div className="flex flex-col gap-2 flex-1">
										<Label className="text-sm" htmlFor={`${id}-workspace-name`}>
											Owner
										</Label>
										<WorkspaceUserAutocomplete
											organizationId={template.organization_id}
											value={owner}
											onChange={(user) => {
												handleOwnerChange(user ?? defaultOwner);
											}}
										/>
									</div>
								)}
							</div>
						</div>
					</section>

					{externalAuth && externalAuth.length > 0 && (
						<section>
							<hgroup>
								<h2 className="text-xl font-semibold m-0">
									External Authentication
								</h2>
								<p className="text-sm text-content-secondary mt-0">
									This template uses external services for authentication.
								</p>
							</hgroup>
							<div className="flex flex-col gap-4">
								{Boolean(error) && !hasAllRequiredExternalAuth && (
									<Alert severity="error" prominent>
										To create a workspace using this template, please connect to
										all required external authentication providers listed below.
									</Alert>
								)}
								{externalAuth.map((auth) => (
									<ExternalAuthButton
										key={auth.id}
										error={error}
										auth={auth}
										isLoading={externalAuthPollingState[auth.id] === "polling"}
										onStartPolling={() => startPollingExternalAuth(auth.id)}
										displayRetry={
											externalAuthPollingState[auth.id] === "abandoned"
										}
									/>
								))}
							</div>
						</section>
					)}

					{parameters.length === 0 && diagnostics.length > 0 && (
						<Diagnostics diagnostics={diagnostics} />
					)}

					{parameters.length > 0 && (
						<section className="flex flex-col gap-9">
							<hgroup>
								<h2 className="text-xl font-semibold m-0">Parameters</h2>
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
							{diagnostics.length > 0 && (
								<Diagnostics diagnostics={diagnostics} />
							)}
							{(editablePrefillPresets.length > 0 ||
								lockedPresets.length > 0) && (
								<div className="flex flex-col gap-3">
									<Label className="text-sm">Preset</Label>
									{editablePrefillPresets.length > 0 && (
										<div
											className="flex flex-wrap gap-2"
											role="group"
											aria-label="Resource presets"
										>
											{editablePrefillPresets.map((preset) => (
												<Button
													key={preset.ID}
													size="sm"
													variant={
														selectedPrefillPresetId === preset.ID
															? "default"
															: "outline"
													}
													aria-pressed={selectedPrefillPresetId === preset.ID}
													onClick={() => {
														setSelectedPresetIndex(0);
														appliedPrefillPresetKey.current = undefined;
														setSelectedPrefillPresetId(preset.ID);
														setPrefillRevision((revision) => revision + 1);
														form.setFieldValue(
															"template_version_preset_id",
															undefined,
														);
													}}
												>
													{preset.Icon && (
														<ExternalImage
															src={preset.Icon}
															alt=""
															className="w-4 h-4"
														/>
													)}
													{preset.Name}
												</Button>
											))}
										</div>
									)}
									{lockedPresets.length > 0 && (
										<div className="max-w-lg">
											<Combobox
												value={presetOptions[selectedPresetIndex]?.value}
												onValueChange={(value) => {
													const index = presetOptions.findIndex(
														(preset) => preset.value === value,
													);
													if (index === -1) {
														return;
													}
													setSelectedPresetIndex(index);
													setSelectedPrefillPresetId(undefined);
													form.setFieldValue(
														"template_version_preset_id",
														presetOptions[index].value === "undefined"
															? undefined
															: presetOptions[index].value,
													);
												}}
											>
												<ComboboxTrigger asChild>
													<ComboboxButton
														selectedOption={{
															label:
																presetOptions[selectedPresetIndex]?.label || "",
															value:
																presetOptions[selectedPresetIndex]?.value || "",
														}}
														placeholder="Select a preset"
													/>
												</ComboboxTrigger>
												<ComboboxContent align="start">
													{presetOptions.map((preset) => (
														<ComboboxItem
															key={preset.value}
															value={preset.value}
														>
															{preset.icon && (
																<ExternalImage
																	src={preset.icon}
																	alt={preset.label}
																	className="w-4 h-4"
																/>
															)}
															{preset.label}
														</ComboboxItem>
													))}
												</ComboboxContent>
											</Combobox>
										</div>
									)}
									{presetParameterNames.length > 0 && (
										<span className="flex items-center gap-3">
											<Switch
												id="show-preset-parameters"
												checked={showPresetParameters}
												onCheckedChange={setShowPresetParameters}
											/>
											<Label htmlFor="show-preset-parameters">
												Show preset parameters
											</Label>
										</span>
									)}
								</div>
							)}

							<div className="flex flex-col gap-9">
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
											form.values.rich_parameter_values?.[minFieldIndex]
												?.value ?? "";
										const maxValue =
											form.values.rich_parameter_values?.[maxFieldIndex]
												?.value ?? "";
										const isPresetParameter =
											presetParameterNames.includes(minParameter.name) ||
											presetParameterNames.includes(maxParameter.name);
										const isDisabledParameter = (parameter: PreviewParameter) =>
											parameterFieldsDisabled ||
											disabledParams?.includes(
												parameter.name.toLowerCase().replace(/ /g, "_"),
											) ||
											parameter.styling?.disabled ||
											creatingWorkspace;
										const isDisabled =
											isDisabledParameter(minParameter) ||
											isDisabledParameter(maxParameter) ||
											isPresetParameter;
										if (
											!showPresetParameters &&
											isPresetParameter &&
											minParameter.diagnostics.length === 0 &&
											maxParameter.diagnostics.length === 0
										) {
											return null;
										}

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
												isPreset={isPresetParameter}
												autofill={
													!isPresetParameter &&
													(autofillByName[minParameter.name] !== undefined ||
														autofillByName[maxParameter.name] !== undefined)
												}
												resetAutoMaxKey={`${selectedPrefillPresetId ?? presetOptions[selectedPresetIndex]?.value ?? ""}:${prefillRevision}`}
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
									const isPresetParameter = presetParameterNames.includes(
										parameter.name,
									);
									const isDisabled =
										parameterFieldsDisabled ||
										disabledParams?.includes(
											parameter.name.toLowerCase().replace(/ /g, "_"),
										) ||
										parameter.styling?.disabled ||
										creatingWorkspace ||
										isPresetParameter;

									if (
										!showPresetParameters &&
										isPresetParameter &&
										parameter.diagnostics.length === 0
									) {
										return null;
									}

									return (
										<DynamicParameter
											key={group.key}
											parameter={parameter}
											onChange={(value) =>
												handleChange(parameter, parameterField, value)
											}
											disabled={isDisabled}
											isPreset={isPresetParameter}
											autofill={
												!isPresetParameter &&
												autofillByName[parameter.name] !== undefined
											}
											value={formValue}
										/>
									);
								})}
							</div>
						</section>
					)}

					<div className="flex flex-row justify-end">
						<Button type="submit" disabled={disabled}>
							<Spinner loading={creatingWorkspace} />
							Create workspace
						</Button>
					</div>
				</form>
			</div>
		</>
	);
};
