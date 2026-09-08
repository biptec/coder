import { RefreshCwIcon } from "lucide-react";
import { type FC, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "react-query";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	createWorkspaceVolumeCopy,
	syncWorkspaceVolumeCopy,
	workspaceActiveVolumeCopyOperation,
	workspaceById,
	workspaceByOwnerAndName,
	workspacePermissions,
	workspaces,
	workspaceVolumeCopyOperation,
	workspaceVolumeCopyVolumes,
} from "#/api/queries/workspaces";
import type { WorkspaceVolumeCopySelection } from "#/api/typesGenerated";
import { Alert } from "#/components/Alert/Alert";
import { Button } from "#/components/Button/Button";
import { Checkbox } from "#/components/Checkbox/Checkbox";
import {
	Combobox,
	ComboboxButton,
	ComboboxContent,
	ComboboxEmpty,
	ComboboxInput,
	ComboboxItem,
	ComboboxList,
	ComboboxTrigger,
} from "#/components/Combobox/Combobox";
import { Loader } from "#/components/Loader/Loader";
import { Margins } from "#/components/Margins/Margins";
import {
	PageHeader,
	PageHeaderSubtitle,
	PageHeaderTitle,
} from "#/components/PageHeader/PageHeader";
import { useDebouncedValue } from "#/hooks/debounce";
import { useDashboard } from "#/modules/dashboard/useDashboard";
import { pageTitle } from "#/utils/page";
import {
	OperationStatus,
	type VolumeChoice,
	type VolumeRow,
	VolumeSelectionList,
} from "./WorkspaceVolumeCopyPageView";

const DESTINATION_SEARCH_LIMIT = 25;

type VolumeCopyDraft = {
	destinationId?: string;
	allowSourceRunning: boolean;
	choices: Record<string, VolumeChoice>;
};

const volumeCopyDraftKey = (workspaceId: string) =>
	`coder:workspace-volume-copy:draft:${workspaceId}`;

const WorkspaceVolumeCopyPage: FC = () => {
	const params = useParams() as { username: string; workspace: string };
	const navigate = useNavigate();
	const [searchParams, setSearchParams] = useSearchParams();
	const dashboard = useDashboard();
	const ownerName = params.username.replace(/^@/, "");
	const workspaceName = params.workspace;

	const sourceQuery = useQuery(
		workspaceByOwnerAndName(ownerName, workspaceName),
	);
	const source = sourceQuery.data;
	const sourcePermissionsQuery = useQuery(workspacePermissions(source));
	const sourceVolumesQuery = useQuery({
		...workspaceVolumeCopyVolumes(source?.id),
		enabled:
			dashboard.appearance.workspace_volume_copy_enabled === true &&
			Boolean(source) &&
			sourcePermissionsQuery.data?.volumeCopyWorkspace === true,
	});

	const [destinationId, setDestinationId] = useState<string>();
	const [destinationSearch, setDestinationSearch] = useState("");
	const debouncedDestinationSearch = useDebouncedValue(destinationSearch, 250);
	const destinationSearchQuery = useQuery({
		...workspaces({
			limit: DESTINATION_SEARCH_LIMIT,
			offset: 0,
			q: debouncedDestinationSearch,
		}),
		enabled: dashboard.appearance.workspace_volume_copy_enabled === true,
	});
	const destinationQuery = useQuery({
		...workspaceById(destinationId ?? ""),
		enabled: Boolean(destinationId),
	});
	const destination = destinationQuery.data;
	const destinationPermissionsQuery = useQuery({
		...workspacePermissions(destination),
		enabled: Boolean(destination),
	});
	const destinationVolumesQuery = useQuery({
		...workspaceVolumeCopyVolumes(destination?.id),
		enabled:
			Boolean(destination) &&
			destinationPermissionsQuery.data?.volumeCopyWorkspace === true,
	});

	const rows = useMemo<VolumeRow[]>(() => {
		const destinationByKey = new Map(
			(destinationVolumesQuery.data ?? []).map((volume) => [
				volume.key,
				volume,
			]),
		);
		return (sourceVolumesQuery.data ?? []).map((sourceVolume) => ({
			source: sourceVolume,
			destination: destinationByKey.get(sourceVolume.key),
		}));
	}, [sourceVolumesQuery.data, destinationVolumesQuery.data]);

	const [choices, setChoices] = useState<Record<string, VolumeChoice>>({});
	const [allowSourceRunning, setAllowSourceRunning] = useState(false);
	const createMutation = useMutation(createWorkspaceVolumeCopy());
	const syncMutation = useMutation(syncWorkspaceVolumeCopy());
	const operationId = searchParams.get("operation") ?? undefined;
	const activeOperationQuery = useQuery({
		...workspaceActiveVolumeCopyOperation(source?.id),
		enabled:
			dashboard.appearance.workspace_volume_copy_enabled === true &&
			Boolean(source) &&
			sourcePermissionsQuery.data?.volumeCopyWorkspace === true,
	});
	const operationQuery = useQuery(workspaceVolumeCopyOperation(operationId));
	const activeOperation = activeOperationQuery.data?.operation;
	const operation = activeOperation ?? operationQuery.data;
	const operationAllowSourceRunning = operation?.allow_source_running;
	const draftLoadedFor = useRef<string | undefined>(undefined);
	const skipNextDraftSaveFor = useRef<string | undefined>(undefined);
	const currentIsOperationDestination = Boolean(
		source && operation?.destination_workspace_id === source.id,
	);
	const operationSourceQuery = useQuery({
		...workspaceById(operation?.source_workspace_id ?? ""),
		enabled: currentIsOperationDestination,
	});

	useEffect(() => {
		if (activeOperation && operationId !== activeOperation.id) {
			setSearchParams({ operation: activeOperation.id }, { replace: true });
		}
	}, [activeOperation, operationId, setSearchParams]);

	useEffect(() => {
		if (!source || operation || draftLoadedFor.current === source.id) {
			return;
		}
		draftLoadedFor.current = source.id;
		try {
			const raw = sessionStorage.getItem(volumeCopyDraftKey(source.id));
			if (!raw) {
				return;
			}
			skipNextDraftSaveFor.current = source.id;
			const draft = JSON.parse(raw) as VolumeCopyDraft;
			setDestinationId(draft.destinationId);
			setAllowSourceRunning(draft.allowSourceRunning ?? false);
			setChoices(draft.choices ?? {});
		} catch {
			sessionStorage.removeItem(volumeCopyDraftKey(source.id));
		}
	}, [operation, source]);

	useEffect(() => {
		if (!source || operation || draftLoadedFor.current !== source.id) {
			return;
		}
		if (skipNextDraftSaveFor.current === source.id) {
			skipNextDraftSaveFor.current = undefined;
			return;
		}
		try {
			sessionStorage.setItem(
				volumeCopyDraftKey(source.id),
				JSON.stringify({
					destinationId,
					allowSourceRunning,
					choices,
				} satisfies VolumeCopyDraft),
			);
		} catch {
			// The form still works when session storage is unavailable.
		}
	}, [allowSourceRunning, choices, destinationId, operation, source]);

	useEffect(() => {
		if (!destination || destinationVolumesQuery.isLoading) {
			return;
		}
		setChoices((current) => {
			const operationChoices = new Map(
				operation?.volumes.map((volume) => [volume.key, volume]) ?? [],
			);
			const next: Record<string, VolumeChoice> = {};
			for (const row of rows) {
				const persisted = current[row.source.key];
				const selected = operationChoices.get(row.source.key);
				next[row.source.key] = selected
					? { copy: true, overwrite: selected.overwrite }
					: row.destination && persisted
						? persisted
						: { copy: Boolean(row.destination), overwrite: false };
			}
			return next;
		});
	}, [destination, destinationVolumesQuery.isLoading, operation, rows]);

	useEffect(() => {
		if (operation && source && operation.source_workspace_id === source.id) {
			setDestinationId(operation.destination_workspace_id);
		}
	}, [operation, source]);

	useEffect(() => {
		if (operationAllowSourceRunning !== undefined) {
			setAllowSourceRunning(operationAllowSourceRunning);
		}
	}, [operationAllowSourceRunning]);

	if (
		sourceQuery.isLoading ||
		sourcePermissionsQuery.isLoading ||
		(dashboard.appearance.workspace_volume_copy_enabled &&
			sourceVolumesQuery.isLoading) ||
		activeOperationQuery.isLoading ||
		(operationId && operationQuery.isLoading)
	) {
		return <Loader fullscreen />;
	}

	if (!dashboard.appearance.workspace_volume_copy_enabled) {
		return (
			<Margins size="medium">
				<PageHeader>
					<PageHeaderTitle>Copy volumes</PageHeaderTitle>
				</PageHeader>
				<Alert severity="warning">
					Workspace volume copying is not enabled on this deployment.
				</Alert>
			</Margins>
		);
	}

	if (!source) {
		return null;
	}

	if (!sourcePermissionsQuery.data?.volumeCopyWorkspace) {
		return (
			<Margins size="medium">
				<PageHeader>
					<PageHeaderTitle>Copy volumes</PageHeaderTitle>
				</PageHeader>
				<Alert severity="error">
					You do not have permission to copy persistent volumes for this
					workspace.
				</Alert>
			</Margins>
		);
	}

	const currentIsOperationSource = Boolean(
		operation && operation.source_workspace_id === source.id,
	);
	if (
		operation &&
		!currentIsOperationSource &&
		!currentIsOperationDestination
	) {
		return (
			<Margins size="medium">
				<PageHeader>
					<PageHeaderTitle>Copy volumes</PageHeaderTitle>
				</PageHeader>
				<Alert severity="error" prominent>
					This volume copy operation belongs to different workspaces.
				</Alert>
			</Margins>
		);
	}

	if (operation && currentIsOperationDestination) {
		const operationSource = operationSourceQuery.data;
		return (
			<>
				<title>{pageTitle(workspaceName, "Copy volumes")}</title>
				<Margins size="medium" className="pb-16">
					<PageHeader>
						<PageHeaderTitle>Copy volumes</PageHeaderTitle>
						<PageHeaderSubtitle>
							This workspace is the destination of an active volume-copy
							operation.
						</PageHeaderSubtitle>
					</PageHeader>
					<div className="flex flex-col gap-6">
						<div className="rounded-lg border border-border-default bg-surface-secondary p-4">
							<div className="text-sm text-content-secondary">Source</div>
							<div className="font-medium">
								{operationSource
									? `${operationSource.owner_name}/${operationSource.name}`
									: operation.source_workspace_id}
							</div>
							<div className="mt-3 text-sm text-content-secondary">
								Destination
							</div>
							<div className="font-medium">
								{source.owner_name}/{source.name}
							</div>
						</div>
						<OperationStatus operation={operation} />
						<div className="flex justify-end">
							<Button variant="outline" onClick={() => navigate("/workspaces")}>
								{operation.status === "pending" ||
								operation.status === "running"
									? "Back"
									: "Done"}
							</Button>
						</div>
					</div>
				</Margins>
			</>
		);
	}

	const sourceStatus = source.latest_build.status;
	const destinationStatus = destination?.latest_build.status;
	const destinationAllowed =
		Boolean(destination) &&
		destinationPermissionsQuery.data?.volumeCopyWorkspace === true;
	const selectedVolumes: WorkspaceVolumeCopySelection[] = rows
		.filter((row) => choices[row.source.key]?.copy && row.destination)
		.map((row) => ({
			key: row.source.key,
			overwrite: choices[row.source.key]?.overwrite ?? false,
		}));
	const sourceStateAllowed = allowSourceRunning
		? sourceStatus === "running" || sourceStatus === "stopped"
		: sourceStatus === "stopped";
	const destinationStateAllowed = destinationStatus === "stopped";
	const formDisabled = Boolean(
		activeOperation ||
			(operation &&
				(operation.status === "pending" || operation.status === "running")),
	);
	const canSubmit =
		!formDisabled &&
		Boolean(destination) &&
		destinationAllowed &&
		destinationStateAllowed &&
		sourceStateAllowed &&
		selectedVolumes.length > 0 &&
		!createMutation.isPending;

	const destinationOptions = (destinationSearchQuery.data?.workspaces ?? [])
		.filter((workspace) => workspace.id !== source.id)
		.sort(
			(a, b) =>
				a.owner_name.localeCompare(b.owner_name, undefined, {
					sensitivity: "base",
				}) || a.name.localeCompare(b.name, undefined, { sensitivity: "base" }),
		);
	const destinationLabel = destination
		? `${destination.owner_name}/${destination.name}`
		: undefined;

	const copy = async () => {
		if (!destination || !canSubmit) {
			return;
		}
		const nextOperation = await createMutation.mutateAsync({
			sourceWorkspaceId: source.id,
			request: {
				destination_workspace_id: destination.id,
				allow_source_running: allowSourceRunning,
				volumes: selectedVolumes,
			},
		});
		try {
			sessionStorage.removeItem(volumeCopyDraftKey(source.id));
		} catch {
			// The copy operation is already durable on the server.
		}
		setSearchParams({ operation: nextOperation.id });
	};

	const syncAgain = async () => {
		if (!operation) {
			return;
		}
		const nextOperation = await syncMutation.mutateAsync(operation.id);
		setSearchParams({ operation: nextOperation.id });
	};

	return (
		<>
			<title>{pageTitle(workspaceName, "Copy volumes")}</title>
			<Margins size="medium" className="pb-16">
				<PageHeader>
					<PageHeaderTitle>Copy volumes</PageHeaderTitle>
					<PageHeaderSubtitle>
						Copy explicitly shared persistent volume contents to another
						workspace without changing workspace ownership.
					</PageHeaderSubtitle>
				</PageHeader>

				<div className="flex flex-col gap-8">
					<section className="flex flex-col gap-2">
						<h3 className="text-base font-medium m-0">Source</h3>
						<div className="rounded-lg border border-border-default bg-surface-secondary p-4">
							<div className="font-medium">
								{source.owner_name}/{source.name}
							</div>
							<div className="text-sm text-content-secondary">
								Status: {sourceStatus}
							</div>
						</div>
					</section>

					<section className="flex flex-col gap-2">
						<label
							className="text-base font-medium"
							htmlFor="volume-copy-destination"
						>
							Destination
						</label>
						<Combobox
							value={destinationId}
							onValueChange={(value) => {
								setDestinationId(value);
								if (operationId) {
									setSearchParams({});
								}
								createMutation.reset();
							}}
						>
							<ComboboxTrigger asChild>
								<ComboboxButton
									id="volume-copy-destination"
									disabled={formDisabled}
									placeholder="Select destination workspace"
									selectedOption={
										destinationLabel
											? { label: destinationLabel, value: destinationId ?? "" }
											: undefined
									}
								/>
							</ComboboxTrigger>
							<ComboboxContent
								shouldFilter={false}
								className="w-[420px] max-w-[calc(100vw-32px)] overflow-hidden"
							>
								<ComboboxInput
									placeholder="Search owner or workspace..."
									value={destinationSearch}
									onValueChange={setDestinationSearch}
								/>
								<ComboboxList className="max-h-[min(24rem,calc(var(--radix-popper-available-height)-2.5rem))] overscroll-contain">
									<ComboboxEmpty>No workspaces found.</ComboboxEmpty>
									{destinationOptions.map((workspace) => (
										<ComboboxItem key={workspace.id} value={workspace.id}>
											<div className="min-w-0 flex-1">
												<div className="truncate">
													{workspace.owner_name}/{workspace.name}
												</div>
												<div className="text-xs text-content-secondary">
													{workspace.latest_build.status}
												</div>
											</div>
										</ComboboxItem>
									))}
								</ComboboxList>
							</ComboboxContent>
						</Combobox>
						{destination && !destinationAllowed && (
							<p className="text-sm text-content-destructive m-0">
								You do not have volume-copy permission on this destination.
							</p>
						)}
						{destination && destinationStatus !== "stopped" && (
							<p className="text-sm text-content-warning m-0">
								Destination must be stopped before copying. Current status:{" "}
								{destinationStatus}.
							</p>
						)}
					</section>

					{destination && destinationAllowed && (
						<section className="flex flex-col gap-3">
							<h3 className="text-base font-medium m-0">Persistent volumes</h3>
							{destinationVolumesQuery.isLoading ? (
								<Loader />
							) : rows.length === 0 ? (
								<Alert severity="warning">
									The source workspace does not expose any copyable persistent
									volumes.
								</Alert>
							) : (
								<VolumeSelectionList
									rows={rows}
									choices={choices}
									disabled={formDisabled}
									onChange={(key, choice) =>
										setChoices((current) => ({
											...current,
											[key]: choice,
										}))
									}
								/>
							)}
						</section>
					)}

					<section className="flex flex-col gap-3">
						<label
							className="flex items-start gap-3 cursor-pointer"
							htmlFor="allow-source-running"
						>
							<Checkbox
								id="allow-source-running"
								checked={allowSourceRunning}
								disabled={formDisabled}
								onCheckedChange={(checked) =>
									setAllowSourceRunning(checked === true)
								}
							/>
							<div>
								<div className="font-medium">
									Allow copying while source workspace is running
								</div>
								<div className="text-sm text-content-secondary">
									When disabled, the source must already be stopped and its
									lifecycle remains locked until the copy finishes.
								</div>
							</div>
						</label>
						{sourceStatus === "running" && allowSourceRunning && (
							<Alert severity="warning" prominent>
								<strong>Source workspace is running.</strong> Files modified
								during the copy may not represent a point-in-time consistent
								snapshot. The source can keep working, but Start/Stop/Delete and
								other lifecycle changes are blocked until the copy finishes.
							</Alert>
						)}
						{!sourceStateAllowed && (
							<Alert severity="warning">
								Source status is <strong>{sourceStatus}</strong>. Stop the
								source first, or enable live copying when the source is running.
							</Alert>
						)}
					</section>

					{(createMutation.error ||
						syncMutation.error ||
						operationQuery.error ||
						activeOperationQuery.error) && (
						<Alert severity="error" prominent>
							<div className="font-medium">
								{getErrorMessage(
									createMutation.error ??
										syncMutation.error ??
										operationQuery.error ??
										activeOperationQuery.error,
									"Volume copy failed",
								)}
							</div>
							<div className="text-sm mt-1">
								{getErrorDetail(
									createMutation.error ??
										syncMutation.error ??
										operationQuery.error ??
										activeOperationQuery.error,
								)}
							</div>
						</Alert>
					)}

					{operation && <OperationStatus operation={operation} />}

					<div className="flex items-center justify-end gap-3 pt-2">
						<Button
							variant="outline"
							onClick={() =>
								navigate(
									operation ? "/workspaces" : `/@${ownerName}/${workspaceName}`,
								)
							}
						>
							{formDisabled ? "Back" : operation ? "Done" : "Cancel"}
						</Button>
						{operation?.status === "succeeded" &&
						operation.allow_source_running ? (
							<Button
								onClick={() => void syncAgain()}
								disabled={syncMutation.isPending || Boolean(activeOperation)}
							>
								<RefreshCwIcon />
								Sync again
							</Button>
						) : (
							<Button onClick={() => void copy()} disabled={!canSubmit}>
								Copy volumes
							</Button>
						)}
					</div>
				</div>
			</Margins>
		</>
	);
};

export default WorkspaceVolumeCopyPage;
