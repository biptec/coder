import Alert from "@mui/material/Alert";
import { ChevronLeftIcon } from "lucide-react";
import type { FC } from "react";
import { Link as RouterLink } from "react-router";
import type * as TypesGen from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Loader } from "#/components/Loader/Loader";
import {
	SettingsHeader,
	SettingsHeaderDescription,
	SettingsHeaderTitle,
} from "#/components/SettingsHeader/SettingsHeader";
import { Stack } from "#/components/Stack/Stack";
import { ExternalAuthProviderForm } from "./ExternalAuthProviderForm";

type EditExternalAuthProviderPageViewProps = {
	provider: TypesGen.ExternalAuthProviderEntry | undefined;
	isLoading: boolean;
	loadError: unknown;
	isSubmitting: boolean;
	submitError: unknown;
	onSubmit: (data: TypesGen.UpdateExternalAuthProviderRequest) => void;
	canEdit: boolean;
};

export const EditExternalAuthProviderPageView: FC<
	EditExternalAuthProviderPageViewProps
> = ({
	provider,
	isLoading,
	loadError,
	isSubmitting,
	submitError,
	onSubmit,
	canEdit,
}) => {
	if (isLoading) {
		return <Loader />;
	}

	if (loadError) {
		return <ErrorAlert error={loadError} />;
	}

	if (!provider) {
		return null;
	}

	const isEnvSourced = provider.source === "env";
	const isDisabled = !canEdit || isEnvSourced;

	return (
		<>
			<Stack
				alignItems="baseline"
				direction="row"
				justifyContent="space-between"
			>
				<SettingsHeader>
					<SettingsHeaderTitle>
						Edit External Auth Provider
					</SettingsHeaderTitle>
					<SettingsHeaderDescription>
						{provider.display_name || provider.provider_id}
					</SettingsHeaderDescription>
				</SettingsHeader>

				<Button variant="outline" asChild>
					<RouterLink to="/deployment/external-auth">
						<ChevronLeftIcon aria-hidden="true" />
						All External Auth Providers
					</RouterLink>
				</Button>
			</Stack>

			<Stack>
				{isEnvSourced && (
					<Alert severity="info">
						This provider is configured via environment variables and
						cannot be edited here. The form below is read-only.
					</Alert>
				)}
				{submitError ? <ErrorAlert error={submitError} /> : undefined}
				<ExternalAuthProviderForm
					initialValues={provider}
					isEditing
					onSubmit={onSubmit}
					isSubmitting={isSubmitting}
					error={submitError}
					disabled={isDisabled}
				/>
			</Stack>
		</>
	);
};
