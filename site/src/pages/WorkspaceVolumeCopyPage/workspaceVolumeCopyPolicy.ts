import type { WorkspaceStatus } from "#/api/typesGenerated";

export const isWorkspaceVolumeCopyDestinationStatusAllowed = (
	status: WorkspaceStatus | undefined,
): boolean => status === "stopped" || status === "running";
