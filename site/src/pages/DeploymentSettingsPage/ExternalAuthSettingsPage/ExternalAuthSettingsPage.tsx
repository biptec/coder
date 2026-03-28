import type { FC } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import {
	deleteExternalAuthProviderConfig,
	externalAuthProviderConfigs,
} from "#/api/queries/externalAuthProviders";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { pageTitle } from "#/utils/page";
import { ExternalAuthSettingsPageView } from "./ExternalAuthSettingsPageView";

const ExternalAuthSettingsPage: FC = () => {
	const { permissions } = useAuthenticated();
	const queryClient = useQueryClient();
	const providersQuery = useQuery(externalAuthProviderConfigs());
	const deleteProviderMutation = useMutation(
		deleteExternalAuthProviderConfig(queryClient),
	);

	const canCreateProvider = permissions.editDeploymentConfig;

	return (
		<>
			<title>{pageTitle("External Authentication")}</title>

			<ExternalAuthSettingsPageView
				providers={providersQuery.data}
				isLoading={providersQuery.isLoading}
				error={providersQuery.error}
				canCreateProvider={canCreateProvider}
				onDeleteProvider={(id) => deleteProviderMutation.mutate(id)}
				deleteProviderLoading={deleteProviderMutation.isPending}
			/>
		</>
	);
};

export default ExternalAuthSettingsPage;
export const Component = ExternalAuthSettingsPage;
