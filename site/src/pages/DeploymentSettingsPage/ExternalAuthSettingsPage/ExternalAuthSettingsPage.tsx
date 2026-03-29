import type { FC } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { toast } from "sonner";
import { getErrorDetail } from "#/api/errors";
import type { ExternalAuthProviderEntry } from "#/api/typesGenerated";
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
				onDeleteProvider={async (provider: ExternalAuthProviderEntry) => {
					const displayName =
						provider.display_name || provider.provider_id;
					const mutation =
						deleteProviderMutation.mutateAsync(provider.id);
					toast.promise(mutation, {
						loading: `Deleting external auth provider "${displayName}"...`,
						success: `External auth provider "${displayName}" deleted.`,
						error: (error) => ({
							message: `Failed to delete external auth provider "${displayName}".`,
							description: getErrorDetail(error),
						}),
					});
					return mutation;
				}}
				deleteProviderLoading={deleteProviderMutation.isPending}
			/>
		</>
	);
};

export default ExternalAuthSettingsPage;
export const Component = ExternalAuthSettingsPage;
