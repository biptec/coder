import type { FC } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { getErrorDetail } from "#/api/errors";
import type { UpdateExternalAuthProviderRequest } from "#/api/typesGenerated";
import {
	externalAuthProviderConfig,
	updateExternalAuthProviderConfig,
} from "#/api/queries/externalAuthProviders";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { pageTitle } from "#/utils/page";
import { EditExternalAuthProviderPageView } from "./EditExternalAuthProviderPageView";

const EditExternalAuthProviderPage: FC = () => {
	const { providerId } = useParams() as { providerId: string };
	const navigate = useNavigate();
	const { permissions } = useAuthenticated();
	const queryClient = useQueryClient();

	const providerQuery = useQuery(externalAuthProviderConfig(providerId));
	const updateMutation = useMutation(
		updateExternalAuthProviderConfig(queryClient),
	);

	const canEdit = permissions.editDeploymentConfig;

	return (
		<>
			<title>{pageTitle("Edit External Auth Provider")}</title>

			<EditExternalAuthProviderPageView
				provider={providerQuery.data}
				isLoading={providerQuery.isLoading}
				loadError={providerQuery.error}
				isSubmitting={updateMutation.isPending}
				submitError={updateMutation.error}
				canEdit={canEdit}
				onSubmit={async (req: UpdateExternalAuthProviderRequest) => {
					const displayName =
						req.display_name || providerQuery.data?.provider_id || providerId;
					const mutation = updateMutation.mutateAsync(
						{ id: providerId, req },
						{
							onSuccess: () => {
								navigate("/deployment/external-auth");
							},
						},
					);
					toast.promise(mutation, {
						loading: `Updating external auth provider "${displayName}"...`,
						success: `External auth provider "${displayName}" updated successfully.`,
						error: (error) => ({
							message: `Failed to update external auth provider "${displayName}".`,
							description: getErrorDetail(error),
						}),
					});
				}}
			/>
		</>
	);
};

export default EditExternalAuthProviderPage;
export const Component = EditExternalAuthProviderPage;
