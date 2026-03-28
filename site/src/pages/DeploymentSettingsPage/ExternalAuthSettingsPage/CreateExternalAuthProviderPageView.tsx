import { ChevronLeftIcon } from "lucide-react";
import type { FC } from "react";
import { Link as RouterLink } from "react-router";
import type * as TypesGen from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import {
	SettingsHeader,
	SettingsHeaderDescription,
	SettingsHeaderTitle,
} from "#/components/SettingsHeader/SettingsHeader";
import { Stack } from "#/components/Stack/Stack";
import { ExternalAuthProviderForm } from "./ExternalAuthProviderForm";

type CreateExternalAuthProviderPageViewProps = {
	isSubmitting: boolean;
	createProvider: (req: TypesGen.CreateExternalAuthProviderRequest) => void;
	error?: unknown;
	canEdit: boolean;
};

export const CreateExternalAuthProviderPageView: FC<
	CreateExternalAuthProviderPageViewProps
> = ({ isSubmitting, createProvider, error, canEdit }) => {
	return (
		<>
			<Stack
				alignItems="baseline"
				direction="row"
				justifyContent="space-between"
			>
				<SettingsHeader>
					<SettingsHeaderTitle>
						Add External Auth Provider
					</SettingsHeaderTitle>
					<SettingsHeaderDescription>
						Configure an external authentication provider for Git or
						third-party services.
					</SettingsHeaderDescription>
				</SettingsHeader>

				<Button variant="outline" asChild>
					<RouterLink to="/deployment/external-auth">
						<ChevronLeftIcon />
						All External Auth Providers
					</RouterLink>
				</Button>
			</Stack>

			<Stack>
				{error ? <ErrorAlert error={error} /> : undefined}
				<ExternalAuthProviderForm
					onSubmit={createProvider}
					isSubmitting={isSubmitting}
					error={error}
					disabled={!canEdit}
				/>
			</Stack>
		</>
	);
};
