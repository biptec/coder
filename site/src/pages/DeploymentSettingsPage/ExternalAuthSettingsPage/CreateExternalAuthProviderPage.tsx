import type { FC } from "react";
import { useMutation, useQueryClient } from "react-query";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { getErrorDetail } from "#/api/errors";
import { createExternalAuthProviderConfig } from "#/api/queries/externalAuthProviders";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import { pageTitle } from "#/utils/page";
import { CreateExternalAuthProviderPageView } from "./CreateExternalAuthProviderPageView";

const CreateExternalAuthProviderPage: FC = () => {
	const navigate = useNavigate();
	const { permissions } = useAuthenticated();
	const queryClient = useQueryClient();
	const createMutation = useMutation(
		createExternalAuthProviderConfig(queryClient),
	);
	const canEdit = permissions.editDeploymentConfig;

	return (
		<>
			<title>{pageTitle("New External Auth Provider")}</title>

			<CreateExternalAuthProviderPageView
				isSubmitting={createMutation.isPending}
				error={createMutation.error}
				createProvider={async (req) => {
					const mutation = createMutation.mutateAsync(req, {
						onSuccess: () => {
							navigate("/deployment/external-auth");
						},
					});
					toast.promise(mutation, {
						loading: `Creating external auth provider "${req.display_name || req.provider_id}"...`,
						success: `External auth provider "${req.display_name || req.provider_id}" created successfully.`,
						error: (error) => ({
							message: `Failed to create external auth provider "${req.display_name || req.provider_id}".`,
							description: getErrorDetail(error),
						}),
					});
				}}
				canEdit={canEdit}
			/>
		</>
	);
};

export default CreateExternalAuthProviderPage;
export const Component = CreateExternalAuthProviderPage;
