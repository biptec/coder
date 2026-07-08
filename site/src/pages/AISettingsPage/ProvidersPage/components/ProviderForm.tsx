import { useFormik } from "formik";
import { TriangleAlertIcon } from "lucide-react";
import { type FC, useEffect, useRef } from "react";
import { Link } from "react-router";
import * as Yup from "yup";
import type { AIProviderType } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import { CodeExample } from "#/components/CodeExample/CodeExample";
import { ConfirmDialog } from "#/components/Dialogs/ConfirmDialog/ConfirmDialog";
import { Form, FormFields } from "#/components/Form/Form";
import { FormField } from "#/components/FormField/FormField";
import { Label } from "#/components/Label/Label";
import { Link as DocsLink } from "#/components/Link/Link";
import { RadioGroup, RadioGroupItem } from "#/components/RadioGroup/RadioGroup";
import { Spinner } from "#/components/Spinner/Spinner";
import { useUnsavedChangesPrompt } from "#/hooks/useUnsavedChangesPrompt";
import { IconPickerField } from "#/pages/AISettingsPage/MCPServersPage/components/IconPickerField";
import { docs } from "#/utils/docs";
import { getFormHelpers } from "#/utils/formUtils";
import { CredentialField } from "./CredentialField";

export type ProviderFormValues = {
	type: AIProviderType | "";
	name: string;
	displayName: string;
	icon: string;
	baseUrl: string;
	model: string;
	smallFastModel: string;
	accessKey: string;
	accessKeySecret: string;
	roleArn: string;
	apiKey: string;
	// wifEnabled selects Workload Identity Federation for an Anthropic
	// provider instead of a static API key. Ignored for other types.
	wifEnabled: boolean;
	federationRuleId: string;
	organizationId: string;
	identityTokenFile: string;
	serviceAccountId: string;
	workspaceId: string;
	enabled: boolean;
};

const HTTP_SCHEME_REGEX = /^https?:\/\//i;
const BEDROCK_CANONICAL_URL_REGEX =
	/^https:\/\/bedrock-runtime\.([a-z0-9-]+)\.amazonaws\.com\/?$/i;
const PROVIDER_NAME_REGEX = /^[a-z0-9]+(-[a-z0-9]+)*$/;

export const SAVED_CREDENTIAL_MASK = "********";

export const parseBedrockRegionFromBaseUrl = (
	baseUrl: string,
): string | undefined => {
	const match = BEDROCK_CANONICAL_URL_REGEX.exec(baseUrl.trim());
	return match?.[1]?.toLowerCase();
};

const makeNameSchema = (editing: boolean) =>
	editing
		? Yup.string()
		: Yup.string()
				.matches(
					PROVIDER_NAME_REGEX,
					"Name must be lowercase, hyphen-separated (e.g. 'my-anthropic').",
				)
				.required("Name is required");

// Display name is always optional. The form copy says blank falls back
// to the provider name, and the update API supports clearing the value.
const makeDisplayNameSchema = (_editing: boolean) => Yup.string();

const defaultInitialValues: ProviderFormValues = {
	type: "anthropic",
	name: "",
	displayName: "",
	icon: "",
	baseUrl: "",
	model: "",
	smallFastModel: "",
	accessKey: "",
	accessKeySecret: "",
	roleArn: "",
	apiKey: "",
	wifEnabled: false,
	federationRuleId: "",
	organizationId: "",
	identityTokenFile: "",
	serviceAccountId: "",
	workspaceId: "",
	enabled: true,
};

// Bedrock model defaults mirror codersdk/deployment.go's
// aiGatewayBedrockModel and aiGatewayBedrockSmallFastModel defaults
// so the create form lands on the same models the env-seeded path
// uses. Update both sides together when AWS publishes new model IDs.
const BEDROCK_DEFAULT_MODEL =
	"global.anthropic.claude-sonnet-4-5-20250929-v1:0";
const BEDROCK_DEFAULT_SMALL_FAST_MODEL =
	"global.anthropic.claude-haiku-4-5-20251001-v1:0";
const BEDROCK_MODEL_CARDS_URL =
	"https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards.html";

const providerDefaults: Partial<
	Record<AIProviderType, Partial<ProviderFormValues>>
> = {
	openai: { name: "openai", baseUrl: "https://api.openai.com/v1/" },
	anthropic: { name: "anthropic", baseUrl: "https://api.anthropic.com" },
	bedrock: {
		name: "bedrock",
		baseUrl: "https://bedrock-runtime.us-east-2.amazonaws.com",
		model: BEDROCK_DEFAULT_MODEL,
		smallFastModel: BEDROCK_DEFAULT_SMALL_FAST_MODEL,
	},
	azure: {
		name: "azure",
		baseUrl: "https://YOUR-RESOURCE.openai.azure.com/openai/v1",
	},
	copilot: {
		name: "copilot",
		baseUrl: "https://api.business.githubcopilot.com",
	},
	google: {
		name: "google",
		baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai/",
	},
	"openai-compat": { name: "openai-compat", baseUrl: "" },
	openrouter: { name: "openrouter", baseUrl: "https://openrouter.ai/api/v1" },
	vercel: { name: "vercel", baseUrl: "https://ai-gateway.vercel.sh/v1" },
};

const baseUrlPlaceholders: Partial<Record<AIProviderType, string>> = {
	"openai-compat": "https://provider.example.com/v1",
};

const makeOpenAiAnthropicSchema = (editing: boolean) =>
	Yup.object({
		type: Yup.string()
			.oneOf([
				"openai",
				"anthropic",
				"azure",
				"google",
				"openai-compat",
				"openrouter",
				"vercel",
			] as const)
			.required(),
		name: makeNameSchema(editing),
		displayName: makeDisplayNameSchema(editing),
		icon: Yup.string(),
		baseUrl: Yup.string()
			.url("Endpoint must be a valid URL")
			.matches(HTTP_SCHEME_REGEX, "Endpoint must use http or https.")
			.required("Endpoint is required"),
		apiKey: editing
			? Yup.string()
			: Yup.string().required("API key is required"),
		enabled: Yup.boolean(),
	});

// WIF exchanges the OIDC identity token and minted access token over the
// endpoint, so cleartext http is refused except for loopback hosts. This
// mirrors codersdk.ValidateAIProviderWIFBaseURL so a form that validates
// here is not rejected by the server.
const WIF_INSECURE_URL_MESSAGE =
	"WIF requires an https endpoint; http is allowed for loopback hosts only.";

const isSecureWIFUrl = (raw: string | undefined): boolean => {
	if (!raw || raw.trim() === "") {
		// The required rule reports the empty case.
		return true;
	}
	let parsed: URL;
	try {
		parsed = new URL(raw.trim());
	} catch {
		// The url() rule reports malformed input.
		return true;
	}
	if (parsed.protocol === "https:") {
		return true;
	}
	if (parsed.protocol === "http:") {
		const host = parsed.hostname.toLowerCase();
		return host === "localhost" || host === "127.0.0.1" || host === "::1";
	}
	return false;
};

// Anthropic Workload Identity Federation replaces the API key with a
// federation rule reference plus the path to the OIDC identity token
// file. federation_rule_id, organization_id, and identity_token_file are
// required (matching codersdk.AIProviderWIFSettings.IsConfigured); the
// service account and workspace IDs are optional.
const makeAnthropicWIFSchema = (editing: boolean) =>
	Yup.object({
		type: Yup.string()
			.oneOf(["anthropic"] as const)
			.required(),
		name: makeNameSchema(editing),
		displayName: makeDisplayNameSchema(editing),
		icon: Yup.string(),
		baseUrl: Yup.string()
			.url("Endpoint must be a valid URL")
			.matches(HTTP_SCHEME_REGEX, "Endpoint must use http or https.")
			.test("wif-secure-url", WIF_INSECURE_URL_MESSAGE, isSecureWIFUrl)
			.required("Endpoint is required"),
		federationRuleId: Yup.string()
			.trim()
			.required("Federation rule ID is required"),
		organizationId: Yup.string().trim().required("Organization ID is required"),
		identityTokenFile: Yup.string()
			.trim()
			.required("Identity token file is required"),
		serviceAccountId: Yup.string(),
		workspaceId: Yup.string(),
		enabled: Yup.boolean(),
	});

const credentialFilled = (value: string | undefined): boolean => {
	if (!value) return false;
	const trimmed = value.trim();
	return trimmed !== "" && trimmed !== SAVED_CREDENTIAL_MASK;
};

const BEDROCK_ACCESS_KEY_PAIRED_MESSAGE =
	"Enter both access key and secret, or leave both blank to use AWS environment credentials.";

// Bedrock access keys are optional: when both are blank the server
// falls back to ambient AWS credentials (IAM role, AWS_PROFILE, IRSA,
// instance profile). Yup still requires them to be supplied as a pair
// so a half-typed rotation does not slip through.
const makeBedrockSchema = (editing: boolean) =>
	Yup.object({
		type: Yup.string()
			.oneOf(["bedrock"] as const)
			.required(),
		name: makeNameSchema(editing),
		displayName: makeDisplayNameSchema(editing),
		icon: Yup.string(),
		baseUrl: Yup.string()
			.url("Endpoint must be a valid URL")
			.matches(
				BEDROCK_CANONICAL_URL_REGEX,
				"Endpoint must be a standard AWS Bedrock URL.",
			)
			.required("Endpoint is required"),
		apiKey: Yup.string(),
		model: Yup.string().required("Model is required"),
		smallFastModel: Yup.string().required("Small-fast model is required"),
		accessKey: Yup.string().test(
			"access-key-paired",
			BEDROCK_ACCESS_KEY_PAIRED_MESSAGE,
			function (value) {
				const secret = (this.parent as { accessKeySecret?: string })
					.accessKeySecret;
				return !(credentialFilled(secret) && !credentialFilled(value));
			},
		),
		accessKeySecret: Yup.string().test(
			"access-key-secret-paired",
			BEDROCK_ACCESS_KEY_PAIRED_MESSAGE,
			function (value) {
				const accessKey = (this.parent as { accessKey?: string }).accessKey;
				return !(credentialFilled(accessKey) && !credentialFilled(value));
			},
		),
		enabled: Yup.boolean(),
	});

const makeCopilotSchema = (editing: boolean) =>
	Yup.object({
		type: Yup.string()
			.oneOf(["copilot"] as const)
			.required(),
		name: makeNameSchema(editing),
		displayName: makeDisplayNameSchema(editing),
		icon: Yup.string(),
		baseUrl: Yup.string()
			.url("Endpoint must be a valid URL")
			.matches(HTTP_SCHEME_REGEX, "Endpoint must use http or https.")
			.required("Endpoint is required"),
		enabled: Yup.boolean(),
	});

const getProviderFormSchema = (editing: boolean) =>
	Yup.lazy(
		(value: { type?: AIProviderType; wifEnabled?: boolean } | undefined) => {
			switch (value?.type) {
				case "anthropic":
					return value?.wifEnabled
						? makeAnthropicWIFSchema(editing)
						: makeOpenAiAnthropicSchema(editing);
				case "openai":
				case "azure":
				case "google":
				case "openai-compat":
				case "openrouter":
				case "vercel":
					return makeOpenAiAnthropicSchema(editing);
				case "bedrock":
					return makeBedrockSchema(editing);
				case "copilot":
					return makeCopilotSchema(editing);
				default:
					return Yup.object({
						type: Yup.string()
							.oneOf([
								"openai",
								"anthropic",
								"bedrock",
								"azure",
								"copilot",
								"google",
								"openai-compat",
								"openrouter",
								"vercel",
							])
							.required(),
					});
			}
		},
	);

type ProviderFormProps = {
	editing?: boolean;
	/** When editing Bedrock and the API already has keys, show masked placeholders until cleared. */
	bedrockSavedAccessCredentials?: boolean;
	/** Server-generated STS external ID, shown read-only when a role is assumed. */
	bedrockExternalId?: string;
	/** When editing openai/anthropic and a key is on file, show a masked placeholder until cleared. */
	openAiAnthropicSavedApiKey?: boolean;
	/** Masked rendering of the saved openai/anthropic key (e.g. `sk-***...ABCD`). Falls back to a generic mask when omitted. */
	openAiAnthropicMaskedApiKey?: string;
	initialValues?: Partial<ProviderFormValues>;
	/** Fires whenever the icon field changes, so page headers can preview it. */
	onIconChange?: (icon: string) => void;
	onSubmit?: (values: ProviderFormValues) => void;
	isLoading?: boolean;
	submitError?: unknown;
};

const namePlaceholder = (provider: string) =>
	providerDefaults[provider as keyof typeof providerDefaults]?.name;

const apiKeyPlaceholder = (provider: string) => {
	switch (provider) {
		case "openai":
			return "sk-proj-...";
		case "anthropic":
			return "sk-ant-...";
	}
};

const baseUrlPlaceholder = (provider: string) =>
	baseUrlPlaceholders[provider as keyof typeof baseUrlPlaceholders] ??
	providerDefaults[provider as keyof typeof providerDefaults]?.baseUrl;

export const ProviderForm: FC<ProviderFormProps> = ({
	editing = false,
	bedrockSavedAccessCredentials = false,
	bedrockExternalId,
	openAiAnthropicSavedApiKey = false,
	openAiAnthropicMaskedApiKey,
	initialValues,
	onIconChange,
	onSubmit,
	isLoading = false,
	submitError,
}) => {
	const resolvedType = initialValues?.type ?? defaultInitialValues.type;
	const typeDefaults =
		providerDefaults[resolvedType as keyof typeof providerDefaults];

	// Seed Bedrock credentials with the mask when on file; focus clears it,
	// and a re-submitted "" tells the API mapping to keep the value.
	const maskedAccessKey = bedrockSavedAccessCredentials
		? SAVED_CREDENTIAL_MASK
		: "";
	const maskedAccessKeySecret = bedrockSavedAccessCredentials
		? SAVED_CREDENTIAL_MASK
		: "";
	// Same pattern for openai/anthropic. Prefer the API-supplied masked
	// rendering so the user sees the key's identifying suffix.
	const maskedApiKey = openAiAnthropicSavedApiKey
		? (openAiAnthropicMaskedApiKey ?? SAVED_CREDENTIAL_MASK)
		: "";

	const didSubmit = useRef(false);
	const form = useFormik<ProviderFormValues>({
		initialValues: {
			...defaultInitialValues,
			// Layer order: base defaults < type prefills < parent's initialValues.
			// Edit overrides prefills with server values; create gets them as-is.
			...(typeDefaults ?? {}),
			...initialValues,
			accessKey: maskedAccessKey,
			accessKeySecret: maskedAccessKeySecret,
			apiKey: maskedApiKey,
		},
		validationSchema: getProviderFormSchema(editing),
		validateOnMount: true,
		onSubmit: (values) => {
			didSubmit.current = true;
			return onSubmit?.(values);
		},
	});
	const getFieldHelpers = getFormHelpers(form, submitError);

	const handleIconChange = (value: string) => {
		void form.setFieldValue("icon", value);
		onIconChange?.(value);
	};

	const iconField = (
		<div className="flex flex-col gap-2">
			<Label htmlFor="icon">Icon</Label>
			<div className="text-xs text-content-secondary">
				Optional. URL or emoji shown for this provider.
			</div>
			<IconPickerField
				id="icon"
				value={form.values.icon}
				onChange={handleIconChange}
			/>
		</div>
	);

	const typeSelectValue = form.values.type;

	// Clears the field once if it's still showing the seeded mask;
	// subsequent focuses are no-ops.
	const handleCredentialFocus = (
		field: "apiKey" | "accessKey" | "accessKeySecret",
	) => {
		const initial = form.initialValues[field];
		if (form.values[field] === initial && initial !== "") {
			void form.setFieldValue(field, "");
		}
	};

	// Restores the mask when the user leaves the field without entering
	// a new value, keeping the saved-credential appearance.
	const handleCredentialBlur = (
		field: "apiKey" | "accessKey" | "accessKeySecret",
	) => {
		const initial = form.initialValues[field];
		if (form.values[field] === "" && initial !== "") {
			void form.setFieldValue(field, initial);
		}
	};

	// When the parent's mutation finishes without an error, treat the just-
	// submitted values as the new baseline so the unsaved-changes prompt does
	// not fire on subsequent navigations. React Query reports a missing error
	// as `null`, so a truthy check covers both null and undefined.
	const previousIsLoading = useRef(isLoading);
	useEffect(() => {
		if (previousIsLoading.current && !isLoading) {
			if (didSubmit.current && !submitError) {
				// Restore credential fields to their initial masked sentinels so
				// the raw key is never left visible after a successful save.
				const remaskedValues = {
					...form.values,
					apiKey: maskedApiKey,
					accessKey: maskedAccessKey,
					accessKeySecret: maskedAccessKeySecret,
				};
				form.resetForm({ values: remaskedValues });
			}
			didSubmit.current = false;
		}
		previousIsLoading.current = isLoading;
	}, [
		isLoading,
		submitError,
		form,
		maskedApiKey,
		maskedAccessKey,
		maskedAccessKeySecret,
	]);

	const unsavedChanges = useUnsavedChangesPrompt(
		form.dirty && !form.isSubmitting,
	);

	return (
		<Form onSubmit={form.handleSubmit}>
			<FormFields>
				{Boolean(submitError) && <ErrorAlert error={submitError} />}
				{typeSelectValue !== "" && typeSelectValue !== "bedrock" && (
					<>
						<div className="grid grid-cols-2 items-start gap-4">
							<FormField
								required
								field={getFieldHelpers("name")}
								label="Name"
								description="Unique identifier (used in urls, can't be changed)"
								className="w-full"
								placeholder={namePlaceholder(form.values.type)}
								disabled={editing}
							/>
							<FormField
								field={getFieldHelpers("displayName")}
								label="Display name"
								description="Friendly name. Defaults to name if blank."
								className="w-full"
							/>
						</div>
						{iconField}
						<FormField
							required
							field={getFieldHelpers("baseUrl")}
							label="Endpoint"
							description={
								typeSelectValue === "copilot" ? (
									<>
										The base URL for your Copilot tier:{" "}
										<code>https://api.individual.githubcopilot.com</code>,{" "}
										<code>https://api.business.githubcopilot.com</code>, or{" "}
										<code>https://api.enterprise.githubcopilot.com</code>.
									</>
								) : (
									"The base URL where the provider's API is hosted."
								)
							}
							className="w-full"
							placeholder={baseUrlPlaceholder(form.values.type)}
						/>
						{typeSelectValue === "copilot" ? (
							<p className="text-sm text-content-secondary m-0">
								Copilot authenticates with each user's GitHub OAuth token at
								request time, so there is no API key to configure here. This
								requires a GitHub external authentication provider to be
								configured.
							</p>
						) : typeSelectValue === "anthropic" ? (
							<>
								<div className="flex flex-col gap-2">
									<Label htmlFor="wif-auth-method">Authentication method</Label>
									<RadioGroup
										id="wif-auth-method"
										value={form.values.wifEnabled ? "wif" : "apikey"}
										onValueChange={(value) => {
											void form.setFieldValue("wifEnabled", value === "wif");
										}}
									>
										<label
											htmlFor="wif-auth-apikey"
											className="flex items-center gap-2 text-sm"
										>
											<RadioGroupItem value="apikey" id="wif-auth-apikey" />
											API key
										</label>
										<label
											htmlFor="wif-auth-wif"
											className="flex items-center gap-2 text-sm"
										>
											<RadioGroupItem value="wif" id="wif-auth-wif" />
											Workload Identity Federation
										</label>
									</RadioGroup>
								</div>
								{form.values.wifEnabled ? (
									<>
										<div className="grid grid-cols-2 items-start gap-4">
											<FormField
												required
												field={getFieldHelpers("federationRuleId")}
												label="Federation rule ID"
												className="w-full"
												placeholder="fdrl_..."
											/>
											<FormField
												required
												field={getFieldHelpers("organizationId")}
												label="Organization ID"
												className="w-full"
												placeholder="00000000-0000-0000-0000-000000000000"
											/>
										</div>
										<FormField
											required
											field={getFieldHelpers("identityTokenFile")}
											label="Identity token file"
											description="Absolute path to the file holding the OIDC identity token (JWT). Re-read on every token exchange."
											className="w-full"
											placeholder="/var/run/secrets/anthropic/token"
										/>
										<div className="grid grid-cols-2 items-start gap-4">
											<FormField
												field={getFieldHelpers("serviceAccountId")}
												label="Service account ID"
												className="w-full"
												placeholder="svac_..."
											/>
											<FormField
												field={getFieldHelpers("workspaceId")}
												label="Workspace ID"
												className="w-full"
												placeholder="wrkspc_... or default"
											/>
										</div>
										<p className="text-xs text-content-secondary m-0">
											Service account ID is required for SERVICE_ACCOUNT-target
											federation rules. Workspace ID is required when the rule
											is enabled for more than one workspace.{" "}
											<DocsLink
												size="sm"
												href={docs("/ai-coder/ai-gateway/providers")}
												target="_blank"
												rel="noreferrer"
											>
												View docs
											</DocsLink>
										</p>
									</>
								) : (
									<CredentialField
										required
										label="API key"
										helpers={getFieldHelpers("apiKey")}
										onBlur={() => handleCredentialBlur("apiKey")}
										onFocus={() => handleCredentialFocus("apiKey")}
										autoComplete="new-password"
										placeholder={apiKeyPlaceholder(form.values.type)}
									/>
								)}
							</>
						) : (
							<CredentialField
								required
								label="API key"
								helpers={getFieldHelpers("apiKey")}
								onBlur={() => handleCredentialBlur("apiKey")}
								onFocus={() => handleCredentialFocus("apiKey")}
								autoComplete="new-password"
								placeholder={apiKeyPlaceholder(form.values.type)}
							/>
						)}
					</>
				)}

				{typeSelectValue === "bedrock" && (
					<>
						<div className="grid grid-cols-2 items-start gap-4">
							<FormField
								required
								field={getFieldHelpers("name")}
								label="Name"
								description="Unique identifier (used in urls, can't be changed)"
								className="w-full"
								placeholder={namePlaceholder(form.values.type)}
								disabled={editing}
							/>
							<FormField
								field={getFieldHelpers("displayName")}
								label="Display name"
								description="Friendly name. Defaults to name if blank."
								className="w-full"
							/>
						</div>
						{iconField}
						<FormField
							required
							field={getFieldHelpers("baseUrl")}
							label="Endpoint"
							description={
								<>
									In the format of{" "}
									<code>
										{"https://bedrock-runtime.{region}.amazonaws.com"}
									</code>
								</>
							}
							className="w-full"
							placeholder={baseUrlPlaceholder(form.values.type)}
						/>
						<div className="grid grid-cols-2 items-start gap-4">
							<FormField
								required
								field={getFieldHelpers("model")}
								label="Model"
								className="w-full"
								placeholder={BEDROCK_DEFAULT_MODEL}
							/>
							<FormField
								required
								field={getFieldHelpers("smallFastModel")}
								label="Small-fast model"
								className="w-full"
								placeholder={BEDROCK_DEFAULT_SMALL_FAST_MODEL}
							/>
						</div>
						<p className="text-xs text-content-secondary m-0">
							Find available Bedrock model IDs in the{" "}
							<DocsLink
								size="sm"
								href={BEDROCK_MODEL_CARDS_URL}
								target="_blank"
								rel="noreferrer"
							>
								AWS Bedrock model cards
							</DocsLink>
							.
						</p>
						<div className="grid grid-cols-2 items-start gap-4">
							<CredentialField
								label="Access key"
								helpers={getFieldHelpers("accessKey")}
								onBlur={() => handleCredentialBlur("accessKey")}
								onFocus={() => handleCredentialFocus("accessKey")}
								autoComplete="new-password"
							/>
							<CredentialField
								label="Access key secret"
								helpers={getFieldHelpers("accessKeySecret")}
								onBlur={() => handleCredentialBlur("accessKeySecret")}
								onFocus={() => handleCredentialFocus("accessKeySecret")}
								autoComplete="new-password"
							/>
						</div>
						<p className="text-xs text-content-secondary m-0">
							Optional. Leave both fields blank to authenticate with the AWS
							environment (IAM role, instance profile, AWS_PROFILE).{" "}
							<DocsLink
								size="sm"
								href={docs("/ai-coder/ai-gateway/providers#amazon-bedrock")}
								target="_blank"
								rel="noreferrer"
							>
								View docs
							</DocsLink>
						</p>
						<FormField
							field={getFieldHelpers("roleArn")}
							label="Role ARN"
							className="w-full"
							placeholder="arn:aws:iam::123456789012:role/BedrockRole"
						/>
						<p className="text-xs text-content-secondary m-0">
							Optional. When a role ARN is set, the gateway assumes that role
							(using the base identity) before calling Bedrock.
						</p>
						{editing && bedrockExternalId && (
							<div className="flex flex-col gap-2">
								<Label>External ID</Label>
								<CodeExample secret={false} code={bedrockExternalId} />
								<p className="text-xs text-content-secondary m-0">
									Server-generated. Add it to the assumed role's trust policy as
									an <code>sts:ExternalId</code> condition so only this
									deployment can assume the role.
								</p>
							</div>
						)}
					</>
				)}

				<div className="flex justify-end gap-4">
					<Link to="/ai/settings/providers">
						<Button variant="outline" type="button">
							Cancel
						</Button>
					</Link>
					<Button
						disabled={isLoading || !form.isValid || (editing && !form.dirty)}
						type="submit"
					>
						<Spinner loading={isLoading} />
						{editing ? "Update provider" : "Add provider"}
					</Button>
				</div>
			</FormFields>
			<ConfirmDialog
				type="info"
				hideCancel={false}
				open={unsavedChanges.isOpen}
				onClose={unsavedChanges.onCancel}
				onConfirm={unsavedChanges.onConfirm}
				title="Unsaved changes"
				confirmText="Confirm"
				description={
					<div className="flex items-start gap-3">
						<TriangleAlertIcon className="size-icon-sm mt-1 shrink-0" />
						<p className="m-0">
							Your updates haven't been saved. Leave anyway?
						</p>
					</div>
				}
			/>
		</Form>
	);
};
