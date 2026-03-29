import Checkbox from "@mui/material/Checkbox";
import FormControlLabel from "@mui/material/FormControlLabel";
import MenuItem from "@mui/material/MenuItem";
import TextField from "@mui/material/TextField";
import { type FC, useState } from "react";
import { Link } from "react-router";
import { isApiValidationError, mapApiErrorToFieldErrors } from "#/api/errors";
import type * as TypesGen from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { Spinner } from "#/components/Spinner/Spinner";

type FormSubmitData =
	| TypesGen.CreateExternalAuthProviderRequest
	| TypesGen.UpdateExternalAuthProviderRequest;

const PROVIDER_PRESETS: Record<
	string,
	{ displayName: string; displayIcon: string }
> = {
	github: { displayName: "GitHub", displayIcon: "/icon/github.svg" },
	gitlab: { displayName: "GitLab", displayIcon: "/icon/gitlab.svg" },
	"bitbucket-cloud": {
		displayName: "Bitbucket Cloud",
		displayIcon: "/icon/bitbucket.svg",
	},
	"bitbucket-server": {
		displayName: "Bitbucket Server",
		displayIcon: "/icon/bitbucket.svg",
	},
	"azure-devops": {
		displayName: "Azure DevOps",
		displayIcon: "/icon/azure-devops.svg",
	},
	"azure-devops-entra": {
		displayName: "Azure DevOps (Entra)",
		displayIcon: "/icon/azure-devops.svg",
	},
	gitea: { displayName: "Gitea", displayIcon: "/icon/gitea.svg" },
	slack: { displayName: "Slack", displayIcon: "/icon/slack.svg" },
	jfrog: { displayName: "JFrog", displayIcon: "/icon/jfrog.svg" },
};

type ExternalAuthProviderFormProps = {
	onSubmit: (data: FormSubmitData) => void;
	error?: unknown;
	isSubmitting: boolean;
	disabled?: boolean;
	initialValues?: TypesGen.ExternalAuthProviderEntry;
	isEditing?: boolean;
};

export const ExternalAuthProviderForm: FC<ExternalAuthProviderFormProps> = ({
	onSubmit,
	error,
	isSubmitting,
	disabled,
	initialValues,
	isEditing = false,
}) => {
	const [selectedType, setSelectedType] = useState(
		initialValues?.type ?? "",
	);
	const [providerId, setProviderId] = useState(
		initialValues?.provider_id ?? "",
	);
	const [displayName, setDisplayName] = useState(
		initialValues?.display_name ?? "",
	);
	const [displayIcon, setDisplayIcon] = useState(
		initialValues?.display_icon ?? "",
	);
	const [deviceFlow, setDeviceFlow] = useState(
		initialValues?.device_flow ?? false,
	);
	const [changeSecret, setChangeSecret] = useState(!isEditing);

	const apiValidationErrors = isApiValidationError(error)
		? mapApiErrorToFieldErrors(error.response.data)
		: undefined;

	// When a preset type is selected, auto-fill fields.
	const handleTypeChange = (value: string) => {
		setSelectedType(value);
		const preset = PROVIDER_PRESETS[value];
		if (preset) {
			if (!isEditing) {
				setProviderId(value);
			}
			setDisplayName(preset.displayName);
			setDisplayIcon(preset.displayIcon);
		}
	};

	// Split a comma-separated string into a trimmed array, filtering
	// out empty entries.
	const splitCommaSeparated = (value: string): string[] => {
		return value
			.split(",")
			.map((s) => s.trim())
			.filter((s) => s.length > 0);
	};

	return (
		<form
			className="mt-2.5"
			onSubmit={(event) => {
				event.preventDefault();
				const formData = new FormData(event.target as HTMLFormElement);

				const sharedFields = {
					type: selectedType || "custom",
					display_name: (formData.get("display_name") as string) || "",
					display_icon: (formData.get("display_icon") as string) || "",
					client_id: (formData.get("client_id") as string) || "",
					auth_url: (formData.get("auth_url") as string) || "",
					token_url: (formData.get("token_url") as string) || "",
					validate_url: (formData.get("validate_url") as string) || "",
					revoke_url: (formData.get("revoke_url") as string) || "",
					device_code_url: (formData.get("device_code_url") as string) || "",
					scopes: splitCommaSeparated(
						(formData.get("scopes") as string) || "",
					),
					extra_token_keys: splitCommaSeparated(
						(formData.get("extra_token_keys") as string) || "",
					),
					no_refresh: formData.get("no_refresh") === "on",
					device_flow: formData.get("device_flow") === "on",
					regex: (formData.get("regex") as string) || "",
					app_install_url:
						(formData.get("app_install_url") as string) || "",
					app_installations_url:
						(formData.get("app_installations_url") as string) || "",
					code_challenge_methods: splitCommaSeparated(
						(formData.get("code_challenge_methods") as string) || "",
					),
				};

				if (isEditing) {
					// In edit mode, only include client_secret when the
					// user explicitly chose to change it.
					const updateData: TypesGen.UpdateExternalAuthProviderRequest = {
						...sharedFields,
						...(changeSecret
							? {
									client_secret:
										(formData.get("client_secret") as string) || "",
								}
							: {}),
					};
					onSubmit(updateData);
				} else {
					onSubmit({
						...sharedFields,
						provider_id: (formData.get("provider_id") as string) || "",
						client_secret: (formData.get("client_secret") as string) || "",
					});
				}
			}}
		>
			<div className="flex flex-col gap-5">
				{/* Provider Type */}
				<TextField
					select
					label="Provider Type"
					value={selectedType}
					onChange={(e) => handleTypeChange(e.target.value)}
					helperText={
						isEditing
							? "The provider type cannot be changed after creation."
							: "Select a preset provider or choose Custom."
					}
					disabled={disabled || isEditing}
					fullWidth
				>
					<MenuItem value="">Custom</MenuItem>
					{Object.entries(PROVIDER_PRESETS).map(([key, preset]) => (
						<MenuItem key={key} value={key}>
							{preset.displayName}
						</MenuItem>
					))}
				</TextField>

				{/* Basic Settings */}
				<TextField
					name="provider_id"
					label="Provider ID"
					required
					value={providerId}
					onChange={(e) => setProviderId(e.target.value)}
					error={Boolean(apiValidationErrors?.provider_id)}
					helperText={
						apiValidationErrors?.provider_id ||
						(isEditing
							? "The provider ID cannot be changed."
							: "A unique identifier for this provider (e.g. github, my-gitlab).")
					}
					disabled={disabled || isEditing}
					autoFocus={!isEditing}
					fullWidth
				/>
				<TextField
					name="display_name"
					label="Display Name"
					value={displayName}
					onChange={(e) => setDisplayName(e.target.value)}
					error={Boolean(apiValidationErrors?.display_name)}
					helperText={
						apiValidationErrors?.display_name ||
						"A human-readable name shown in the UI."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="display_icon"
					label="Display Icon URL"
					value={displayIcon}
					onChange={(e) => setDisplayIcon(e.target.value)}
					error={Boolean(apiValidationErrors?.display_icon)}
					helperText={
						apiValidationErrors?.display_icon ||
						"A full or relative URL to the provider icon."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="client_id"
					label="Client ID"
					required
					defaultValue={initialValues?.client_id}
					error={Boolean(apiValidationErrors?.client_id)}
					helperText={
						apiValidationErrors?.client_id ||
						"The OAuth2 client ID for this provider."
					}
					disabled={disabled}
					fullWidth
				/>
				{isEditing && !changeSecret ? (
					<div className="flex items-center gap-2">
						<TextField
							disabled
							fullWidth
							label="Client Secret"
							value="••••••••"
							helperText="The existing secret is preserved unless you change it."
						/>
						<Button
							variant="outline"
							type="button"
							disabled={disabled}
							onClick={() => setChangeSecret(true)}
						>
							Change
						</Button>
					</div>
				) : (
					<TextField
						name="client_secret"
						label="Client Secret"
						type="password"
						autoFocus={isEditing && changeSecret}
						error={Boolean(apiValidationErrors?.client_secret)}
						helperText={
							apiValidationErrors?.client_secret ||
							"The OAuth2 client secret for this provider."
						}
						disabled={disabled}
						fullWidth
					/>
				)}

				{/* Endpoints */}
				<h3 className="m-0 text-sm font-medium text-content-secondary">
					Endpoints
				</h3>
				<TextField
					name="auth_url"
					label="Auth URL"
					defaultValue={initialValues?.auth_url}
					error={Boolean(apiValidationErrors?.auth_url)}
					helperText={
						apiValidationErrors?.auth_url ||
						"The authorization endpoint URL."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="token_url"
					label="Token URL"
					defaultValue={initialValues?.token_url}
					error={Boolean(apiValidationErrors?.token_url)}
					helperText={
						apiValidationErrors?.token_url || "The token endpoint URL."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="validate_url"
					label="Validate URL"
					defaultValue={initialValues?.validate_url}
					error={Boolean(apiValidationErrors?.validate_url)}
					helperText={
						apiValidationErrors?.validate_url ||
						"The URL used to validate tokens."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="revoke_url"
					label="Revoke URL"
					defaultValue={initialValues?.revoke_url}
					error={Boolean(apiValidationErrors?.revoke_url)}
					helperText={
						apiValidationErrors?.revoke_url ||
						"The URL used to revoke tokens."
					}
					disabled={disabled}
					fullWidth
				/>

				{/* Scopes & Matching */}
				<h3 className="m-0 text-sm font-medium text-content-secondary">
					Scopes & Matching
				</h3>
				<TextField
					name="scopes"
					label="Scopes"
					defaultValue={initialValues?.scopes?.join(", ")}
					error={Boolean(apiValidationErrors?.scopes)}
					helperText={
						apiValidationErrors?.scopes ||
						"Comma-separated list of OAuth2 scopes to request."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="regex"
					label="Regex"
					defaultValue={initialValues?.regex}
					error={Boolean(apiValidationErrors?.regex)}
					helperText={
						apiValidationErrors?.regex ||
						"A regex pattern to match repository URLs for this provider."
					}
					disabled={disabled}
					fullWidth
				/>

				{/* Device Flow */}
				<h3 className="m-0 text-sm font-medium text-content-secondary">
					Device Flow
				</h3>
				<FormControlLabel
					control={
						<Checkbox
							name="device_flow"
							checked={deviceFlow}
							onChange={(e) => setDeviceFlow(e.target.checked)}
							disabled={disabled}
						/>
					}
					label="Enable device flow"
				/>
				{deviceFlow && (
					<TextField
						name="device_code_url"
						label="Device Code URL"
						defaultValue={initialValues?.device_code_url}
						error={Boolean(apiValidationErrors?.device_code_url)}
						helperText={
							apiValidationErrors?.device_code_url ||
							"The device code endpoint URL."
						}
						disabled={disabled}
						fullWidth
					/>
				)}

				{/* Advanced */}
				<h3 className="m-0 text-sm font-medium text-content-secondary">
					Advanced
				</h3>
				<FormControlLabel
					control={
						<Checkbox
							name="no_refresh"
							defaultChecked={initialValues?.no_refresh}
							disabled={disabled}
						/>
					}
					label="No Refresh (disable token refresh)"
				/>
				<TextField
					name="extra_token_keys"
					label="Extra Token Keys"
					defaultValue={initialValues?.extra_token_keys?.join(", ")}
					error={Boolean(apiValidationErrors?.extra_token_keys)}
					helperText={
						apiValidationErrors?.extra_token_keys ||
						"Comma-separated list of extra keys to extract from the token response."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="code_challenge_methods"
					label="Code Challenge Methods"
					defaultValue={initialValues?.code_challenge_methods?.join(", ")}
					error={Boolean(apiValidationErrors?.code_challenge_methods)}
					helperText={
						apiValidationErrors?.code_challenge_methods ||
						"Comma-separated list of PKCE code challenge methods (e.g. S256)."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="app_install_url"
					label="App Install URL"
					defaultValue={initialValues?.app_install_url}
					error={Boolean(apiValidationErrors?.app_install_url)}
					helperText={
						apiValidationErrors?.app_install_url ||
						"The URL to install the application (e.g. GitHub App install URL)."
					}
					disabled={disabled}
					fullWidth
				/>
				<TextField
					name="app_installations_url"
					label="App Installations URL"
					defaultValue={initialValues?.app_installations_url}
					error={Boolean(apiValidationErrors?.app_installations_url)}
					helperText={
						apiValidationErrors?.app_installations_url ||
						"The API URL to list app installations."
					}
					disabled={disabled}
					fullWidth
				/>

				<div className="flex flex-row gap-4">
					<Button
						disabled={isSubmitting || disabled}
						type="submit"
					>
						<Spinner loading={isSubmitting} />
						{isEditing ? "Save changes" : "Create provider"}
					</Button>
					<Button variant="outline" asChild>
						<Link to="/deployment/external-auth">Cancel</Link>
					</Button>
				</div>
			</div>
		</form>
	);
};
