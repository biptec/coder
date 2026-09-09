import dayjs from "dayjs";
import { ChevronDownIcon, TrashIcon } from "lucide-react";
import { type FC, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { useParams } from "react-router";
import { toast } from "sonner";
import { watchWorkspaceActivity } from "#/api/api";
import { getErrorMessage } from "#/api/errors";
import {
	deleteWorkspaceCommandActivity,
	workspaceByOwnerAndName,
	workspaceCommandActivity,
} from "#/api/queries/workspaces";
import type {
	ServerSentEvent,
	WorkspaceActivityWatchEvent,
	WorkspaceCommandActivity,
	WorkspaceCommandActivityFilter,
	WorkspaceCommandActivityRequest,
	WorkspaceCommandActivityResponse,
	WorkspaceCommandActivitySort,
	WorkspaceCommandActivitySortDirection,
	WorkspaceCommandActivitySource,
	WorkspaceCommandActivityStatus,
	WorkspaceMCPRequestActivity,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Badge } from "#/components/Badge/Badge";
import { Button } from "#/components/Button/Button";
import { Checkbox } from "#/components/Checkbox/Checkbox";
import { CopyableValue } from "#/components/CopyableValue/CopyableValue";
import { ConfirmDialog } from "#/components/Dialogs/ConfirmDialog/ConfirmDialog";
import { DeleteDialog } from "#/components/Dialogs/DeleteDialog/DeleteDialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "#/components/DropdownMenu/DropdownMenu";
import { EmptyState } from "#/components/EmptyState/EmptyState";
import { Loader } from "#/components/Loader/Loader";
import { Margins } from "#/components/Margins/Margins";
import {
	PageHeader,
	PageHeaderSubtitle,
	PageHeaderTitle,
} from "#/components/PageHeader/PageHeader";
import { PaginationWidgetBase } from "#/components/PaginationWidget/PaginationWidgetBase";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "#/components/Select/Select";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "#/components/Table/Table";
import { TableToolbar } from "#/components/TableToolbar/TableToolbar";
import { pageTitle } from "#/utils/page";
import {
	type ActivityFilterOption,
	MultiSelectColumnFilter,
	RangeColumnFilter,
	TextColumnFilter,
} from "./ActivityColumnFilter";
import {
	type ActivityDisplayRow,
	activityInput,
	activitySourceLabel,
	buildActivityDisplayRows,
} from "./activityDisplay";
import {
	type ActivityDisplayStatus,
	type ActivityVisibleSource,
	activitySourceOptions,
	activityStatusOptions,
	defaultRealActivityStatuses,
	defaultWorkspaceActivityPreferences,
	loadWorkspaceActivityPreferences,
	activityPageSizeOptions as pageSizeOptions,
	saveWorkspaceActivityPreferences,
} from "./activityPreferences";
import {
	updateCommandActivityCache,
	updateMCPRequestActivityCache,
} from "./commandActivityCache";

type ParsedDuration = {
	value?: number;
	error?: string;
};

type ParsedExitCode = {
	value?: number;
	error?: string;
};

const statusOptions: readonly ActivityFilterOption[] =
	activityStatusOptions.map((status) => ({
		value: status,
		label: status.charAt(0).toUpperCase() + status.slice(1),
	}));

const sourceLabels: Record<ActivityVisibleSource, string> = {
	mcp: "MCP",
	ssh: "SSH",
	reconnecting_pty: "Web terminal",
	chat: "Agent Chat",
};

const sourceFilterOptions: readonly ActivityFilterOption[] =
	activitySourceOptions.map((source) => ({
		value: source,
		label: sourceLabels[source],
	}));

const WorkspaceCommandActivityPage: FC = () => {
	const params = useParams() as { username: string; workspace: string };
	const username = params.username.replace("@", "");
	const workspaceName = params.workspace;
	const workspaceQuery = useQuery(
		workspaceByOwnerAndName(username, workspaceName),
	);
	const workspaceId = workspaceQuery.data?.id;

	const [initialPreferences] = useState(loadWorkspaceActivityPreferences);
	const [idFilter, setIDFilter] = useState(initialPreferences.id);
	const [inputFilter, setInputFilter] = useState(initialPreferences.input);
	const [statuses, setStatuses] = useState<ActivityDisplayStatus[]>(
		initialPreferences.statuses,
	);
	const [tools, setTools] = useState<string[] | null>(initialPreferences.tools);
	const [sources, setSources] = useState<ActivityVisibleSource[]>(
		initialPreferences.sources,
	);
	const [startedAfter, setStartedAfter] = useState(
		initialPreferences.startedAfter,
	);
	const [startedBefore, setStartedBefore] = useState(
		initialPreferences.startedBefore,
	);
	const [durationMin, setDurationMin] = useState(
		initialPreferences.durationMin,
	);
	const [durationMax, setDurationMax] = useState(
		initialPreferences.durationMax,
	);
	const [exitCode, setExitCode] = useState(initialPreferences.exitCode);
	const [sortBy, setSortBy] = useState<WorkspaceCommandActivitySort>(
		initialPreferences.sortBy,
	);
	const [sortDirection, setSortDirection] =
		useState<WorkspaceCommandActivitySortDirection>(
			initialPreferences.sortDirection,
		);
	const [page, setPage] = useState(1);
	const [pageSize, setPageSize] = useState(initialPreferences.pageSize);
	const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
	const [deleteSelectedOpen, setDeleteSelectedOpen] = useState(false);
	const [deleteAllOpen, setDeleteAllOpen] = useState(false);

	useEffect(() => {
		saveWorkspaceActivityPreferences({
			id: idFilter,
			input: inputFilter,
			statuses,
			tools,
			sources,
			startedAfter,
			startedBefore,
			durationMin,
			durationMax,
			exitCode,
			sortBy,
			sortDirection,
			pageSize,
		});
	}, [
		idFilter,
		inputFilter,
		statuses,
		tools,
		sources,
		startedAfter,
		startedBefore,
		durationMin,
		durationMax,
		exitCode,
		sortBy,
		sortDirection,
		pageSize,
	]);

	const parsedMinDuration = useMemo(
		() => parseDurationFilter(durationMin, "Minimum duration"),
		[durationMin],
	);
	const parsedMaxDuration = useMemo(
		() => parseDurationFilter(durationMax, "Maximum duration"),
		[durationMax],
	);
	const parsedExitCode = useMemo(
		() => parseExitCodeFilter(exitCode),
		[exitCode],
	);
	const durationRangeError =
		parsedMinDuration.value !== undefined &&
		parsedMaxDuration.value !== undefined &&
		parsedMinDuration.value > parsedMaxDuration.value
			? "Minimum duration cannot exceed maximum duration."
			: undefined;
	const startedRangeError =
		startedAfter &&
		startedBefore &&
		new Date(startedAfter).getTime() > new Date(startedBefore).getTime()
			? "Started from cannot be after started to."
			: undefined;
	const filterError =
		parsedMinDuration.error ??
		parsedMaxDuration.error ??
		durationRangeError ??
		startedRangeError ??
		parsedExitCode.error;

	const includeIdle = statuses.includes("idle");
	const realStatuses = useMemo(
		() =>
			statuses.filter(
				(status) => status !== "idle",
			) as WorkspaceCommandActivityStatus[],
		[statuses],
	);
	const hasToolSelection = tools === null || tools.length > 0;
	const hasSourceSelection = sources.length > 0;
	const showRealActivity =
		realStatuses.length > 0 && hasToolSelection && hasSourceSelection;
	const queryStatuses = useMemo<WorkspaceCommandActivityStatus[]>(() => {
		if (realStatuses.length > 0) return realStatuses;
		return includeIdle ? [...defaultRealActivityStatuses] : [];
	}, [realStatuses, includeIdle]);
	const queryEnabled =
		queryStatuses.length > 0 &&
		(includeIdle || (hasToolSelection && hasSourceSelection)) &&
		!filterError;

	const filter = useMemo<WorkspaceCommandActivityFilter>(
		() => ({
			id: idFilter.trim() || undefined,
			statuses: queryStatuses,
			tools: tools ?? undefined,
			sources: sources as WorkspaceCommandActivitySource[],
			search: inputFilter.trim() || undefined,
			started_after: toISOString(startedAfter),
			started_before: toISOString(startedBefore),
			duration_min_ms: parsedMinDuration.value,
			duration_max_ms: parsedMaxDuration.value,
			exit_code: parsedExitCode.value,
		}),
		[
			idFilter,
			queryStatuses,
			tools,
			sources,
			inputFilter,
			startedAfter,
			startedBefore,
			parsedMinDuration.value,
			parsedMaxDuration.value,
			parsedExitCode.value,
		],
	);
	const commandRequest = useMemo<WorkspaceCommandActivityRequest>(
		() => ({
			...filter,
			// Idle-only views are timelines even when a stored preference points at
			// a command-only sort column such as Tool or Exit.
			sort_by: showRealActivity ? sortBy : "started",
			sort_direction: showRealActivity ? sortDirection : "desc",
			page,
			page_size: pageSize,
			include_idle: includeIdle,
		}),
		[
			filter,
			sortBy,
			sortDirection,
			showRealActivity,
			page,
			pageSize,
			includeIdle,
		],
	);

	const commandQuery = useQuery({
		...workspaceCommandActivity(workspaceId, commandRequest),
		enabled: Boolean(workspaceId) && queryEnabled,
	});
	const toolCatalogQuery = useQuery({
		...workspaceCommandActivity(workspaceId, {
			statuses: [...defaultRealActivityStatuses],
			sort_by: "started",
			sort_direction: "desc",
			page: 1,
			page_size: 1,
		}),
		enabled: Boolean(workspaceId) && !queryEnabled,
	});
	const queryClient = useQueryClient();
	const deleteMutation = useMutation({
		...deleteWorkspaceCommandActivity(),
		onSuccess: (response) => {
			toast.success(
				`Deleted ${response.deleted} activity history ${response.deleted === 1 ? "record" : "records"}.`,
			);
			setSelectedIds(new Set());
			setDeleteSelectedOpen(false);
			setDeleteAllOpen(false);
			setPage(1);
			void queryClient.invalidateQueries({
				queryKey: ["workspaces", workspaceId, "command-activity"],
			});
		},
		onError: (error: unknown) => {
			toast.error(getErrorMessage(error, "Failed to delete activity history."));
		},
	});

	const availableTools = useMemo(() => {
		const names = new Set([
			...(commandQuery.data?.available_tools ?? []),
			...(toolCatalogQuery.data?.available_tools ?? []),
		]);
		for (const name of tools ?? []) names.add(name);
		return Array.from(names).sort();
	}, [
		commandQuery.data?.available_tools,
		toolCatalogQuery.data?.available_tools,
		tools,
	]);
	const toolFilterOptions = useMemo<ActivityFilterOption[]>(
		() => availableTools.map((tool) => ({ value: tool, label: tool })),
		[availableTools],
	);

	const realActivity = queryEnabled ? (commandQuery.data?.activity ?? []) : [];
	const mcpRequests = queryEnabled
		? (commandQuery.data?.mcp_requests ?? [])
		: [];
	const displayRows = useMemo(
		() =>
			buildActivityDisplayRows(
				realActivity,
				mcpRequests,
				includeIdle,
				showRealActivity,
				sortBy,
				sortDirection,
			),
		[
			realActivity,
			mcpRequests,
			includeIdle,
			showRealActivity,
			sortBy,
			sortDirection,
		],
	);
	const selectableActivity = showRealActivity ? realActivity : [];
	const currentPageIds = useMemo(
		() => new Set(selectableActivity.map((item) => item.id)),
		[selectableActivity],
	);
	const allCurrentPageSelected =
		selectableActivity.length > 0 &&
		selectableActivity.every((item) => selectedIds.has(item.id));
	useEffect(() => {
		setSelectedIds((current) => {
			const retained = new Set(
				Array.from(current).filter((id) => currentPageIds.has(id)),
			);
			return retained.size === current.size ? current : retained;
		});
	}, [currentPageIds]);
	const totalCount = queryEnabled ? (commandQuery.data?.total_count ?? 0) : 0;
	const deletableCount = queryEnabled
		? (commandQuery.data?.deletable_count ?? 0)
		: 0;
	const totalPages = Math.max(
		1,
		queryEnabled ? (commandQuery.data?.total_pages ?? 0) : 0,
	);
	const hasActiveFilter =
		Boolean(idFilter.trim()) ||
		Boolean(inputFilter.trim()) ||
		!hasDefaultStatuses(statuses) ||
		tools !== null ||
		sources.length !== activitySourceOptions.length ||
		Boolean(startedAfter) ||
		Boolean(startedBefore) ||
		Boolean(durationMin.trim()) ||
		Boolean(durationMax.trim()) ||
		Boolean(exitCode.trim());
	const canDeleteAllMatching =
		showRealActivity && deletableCount > 0 && !filterError && queryEnabled;

	useEffect(() => {
		if (page > totalPages) setPage(totalPages);
	}, [page, totalPages]);

	useEffect(() => {
		if (!workspaceId) return;
		let disposed = false;
		let reconnectTimer: number | undefined;
		let resyncTimer: number | undefined;
		let activeSocket: ReturnType<typeof watchWorkspaceActivity> | undefined;
		const commandKey = ["workspaces", workspaceId, "command-activity"] as const;
		const scheduleCommandResync = () => {
			if (resyncTimer !== undefined) window.clearTimeout(resyncTimer);
			resyncTimer = window.setTimeout(() => {
				void queryClient.invalidateQueries({ queryKey: commandKey });
			}, 150);
		};
		const handleCommandUpsert = (command: WorkspaceCommandActivity) => {
			let needsResync = false;
			const cached =
				queryClient.getQueriesData<WorkspaceCommandActivityResponse>({
					queryKey: commandKey,
				});
			for (const [queryKey, data] of cached) {
				if (!data || !Array.isArray(queryKey)) continue;
				const request = (queryKey[3] ?? {}) as WorkspaceCommandActivityRequest;
				const update = updateCommandActivityCache(data, command, request);
				if (update === undefined) {
					needsResync = true;
					continue;
				}
				if (update !== data) queryClient.setQueryData(queryKey, update);
			}
			if (needsResync) scheduleCommandResync();
		};
		const handleMCPRequestUpsert = (
			requestActivity: WorkspaceMCPRequestActivity,
		) => {
			const cached =
				queryClient.getQueriesData<WorkspaceCommandActivityResponse>({
					queryKey: commandKey,
				});
			for (const [queryKey, data] of cached) {
				if (!data || !Array.isArray(queryKey)) continue;
				const request = (queryKey[3] ?? {}) as WorkspaceCommandActivityRequest;
				const update = updateMCPRequestActivityCache(
					data,
					requestActivity,
					request,
				);
				if (update !== data) queryClient.setQueryData(queryKey, update);
			}
		};
		const connect = () => {
			if (disposed) return;
			const socket = watchWorkspaceActivity(workspaceId);
			activeSocket = socket;
			socket.addEventListener("message", (event) => {
				if (event.parseError || !event.parsedMessage) return;
				const envelope = event.parsedMessage as ServerSentEvent;
				if (envelope.type === "ping") {
					void queryClient.invalidateQueries({ queryKey: commandKey });
					return;
				}
				if (envelope.type !== "data") return;
				const payload = envelope.data as WorkspaceActivityWatchEvent;
				switch (payload.type) {
					case "command_upsert":
						if (payload.command) handleCommandUpsert(payload.command);
						break;
					case "command_resync":
						scheduleCommandResync();
						break;
					case "mcp_request_upsert":
						if (payload.mcp_request)
							handleMCPRequestUpsert(payload.mcp_request);
						break;
				}
			});
			const reconnect = () => {
				if (disposed || reconnectTimer !== undefined) return;
				reconnectTimer = window.setTimeout(() => {
					reconnectTimer = undefined;
					connect();
				}, 1_000);
			};
			socket.addEventListener("close", reconnect);
			socket.addEventListener("error", () => {
				socket.close();
				reconnect();
			});
		};
		connect();
		return () => {
			disposed = true;
			if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer);
			if (resyncTimer !== undefined) window.clearTimeout(resyncTimer);
			activeSocket?.close();
		};
	}, [workspaceId, queryClient]);

	if (workspaceQuery.isLoading) return <Loader />;
	if (workspaceQuery.error) {
		return (
			<Margins className="py-8">
				<ErrorAlert error={workspaceQuery.error} />
			</Margins>
		);
	}

	const resetFilters = () => {
		const defaults = defaultWorkspaceActivityPreferences();
		setIDFilter(defaults.id);
		setInputFilter(defaults.input);
		setStatuses(defaults.statuses);
		setTools(defaults.tools);
		setSources(defaults.sources);
		setStartedAfter(defaults.startedAfter);
		setStartedBefore(defaults.startedBefore);
		setDurationMin(defaults.durationMin);
		setDurationMax(defaults.durationMax);
		setExitCode(defaults.exitCode);
		setSelectedIds(new Set());
		setPage(1);
	};
	const filtersChanged = () => {
		setSelectedIds(new Set());
		setPage(1);
	};
	const updateSort = (column: WorkspaceCommandActivitySort) => {
		setSelectedIds(new Set());
		if (sortBy === column) {
			setSortDirection((current) => (current === "asc" ? "desc" : "asc"));
		} else {
			setSortBy(column);
			setSortDirection(column === "started" ? "desc" : "asc");
		}
		setPage(1);
	};
	const deleteSelected = () => {
		if (!workspaceId || selectedIds.size === 0) return;
		deleteMutation.mutate({
			workspaceId,
			request: { mode: "selected", ids: Array.from(selectedIds) },
		});
	};
	const deleteAllMatching = () => {
		if (!workspaceId || !canDeleteAllMatching) return;
		deleteMutation.mutate({
			workspaceId,
			request: { mode: "filtered", filter },
		});
	};

	return (
		<>
			<title>{pageTitle(`Activity history · ${workspaceName}`)}</title>
			<Margins className="pb-12">
				<PageHeader>
					<PageHeaderTitle>Activity history</PageHeaderTitle>
					<PageHeaderSubtitle>
						Live commands and tool requests for @{username}/{workspaceName}.
					</PageHeaderSubtitle>
				</PageHeader>

				<TableToolbar>
					<div className="flex w-full items-center gap-2">
						{selectedIds.size > 0 && (
							<div>
								Selected <strong>{selectedIds.size}</strong>
							</div>
						)}
						<div className="ml-auto flex items-center gap-2">
							{hasActiveFilter && (
								<Button variant="subtle" size="sm" onClick={resetFilters}>
									Reset filters
								</Button>
							)}
							<Button
								variant="outline"
								size="sm"
								disabled={!canDeleteAllMatching || deleteMutation.isPending}
								onClick={() => setDeleteAllOpen(true)}
							>
								Delete all matching
							</Button>
							{selectedIds.size > 0 && (
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<Button
											variant="outline"
											size="sm"
											disabled={deleteMutation.isPending}
										>
											Bulk actions
											<ChevronDownIcon />
										</Button>
									</DropdownMenuTrigger>
									<DropdownMenuContent align="end">
										<DropdownMenuItem
											className="text-content-destructive focus:text-content-destructive"
											onClick={() => setDeleteSelectedOpen(true)}
										>
											<TrashIcon /> Delete&hellip;
										</DropdownMenuItem>
									</DropdownMenuContent>
								</DropdownMenu>
							)}
						</div>
					</div>
				</TableToolbar>

				{commandQuery.error ? (
					<ErrorAlert error={commandQuery.error} />
				) : filterError ? (
					<div className="mb-3 rounded-md border border-border-default p-4 text-sm text-content-destructive">
						{filterError}
					</div>
				) : commandQuery.isLoading && queryEnabled ? (
					<Loader />
				) : (
					<ActivityHistoryTable
						rows={displayRows}
						realActivity={selectableActivity}
						selectedIds={selectedIds}
						allCurrentPageSelected={allCurrentPageSelected}
						sortBy={sortBy}
						sortDirection={sortDirection}
						idFilter={idFilter}
						statuses={statuses}
						startedAfter={startedAfter}
						startedBefore={startedBefore}
						durationMin={durationMin}
						durationMax={durationMax}
						tools={tools}
						toolOptions={toolFilterOptions}
						sources={sources}
						inputFilter={inputFilter}
						exitCode={exitCode}
						onSort={updateSort}
						onID={(value) => {
							setIDFilter(value);
							filtersChanged();
						}}
						onStatuses={(value) => {
							setStatuses(value);
							filtersChanged();
						}}
						onStarted={(after, before) => {
							setStartedAfter(after);
							setStartedBefore(before);
							filtersChanged();
						}}
						onDuration={(min, max) => {
							setDurationMin(min);
							setDurationMax(max);
							filtersChanged();
						}}
						onTools={(value) => {
							setTools(value);
							filtersChanged();
						}}
						onSources={(value) => {
							setSources(value);
							filtersChanged();
						}}
						onInput={(value) => {
							setInputFilter(value);
							filtersChanged();
						}}
						onExit={(value) => {
							setExitCode(value);
							filtersChanged();
						}}
						onSelectPage={(checked) => {
							setSelectedIds((current) => {
								const next = new Set(current);
								for (const id of currentPageIds) {
									if (checked) next.add(id);
									else next.delete(id);
								}
								return next;
							});
						}}
						onSelect={(id, checked) => {
							setSelectedIds((current) => {
								const next = new Set(current);
								if (checked) next.add(id);
								else next.delete(id);
								return next;
							});
						}}
					/>
				)}

				<ActivityPagination
					page={page}
					pageSize={pageSize}
					totalCount={totalCount}
					totalPages={totalPages}
					onPage={(value) => {
						setSelectedIds(new Set());
						setPage(value);
					}}
					onPageSize={(value) => {
						setSelectedIds(new Set());
						setPageSize(value);
						setPage(1);
					}}
				/>
			</Margins>

			<ConfirmDialog
				type="delete"
				open={deleteSelectedOpen}
				onClose={() => setDeleteSelectedOpen(false)}
				onConfirm={deleteSelected}
				confirmLoading={deleteMutation.isPending}
				title={`Delete ${selectedIds.size} selected activity ${selectedIds.size === 1 ? "record" : "records"}?`}
				confirmText="Delete"
				description={
					<>
						<p>
							This permanently deletes the selected activity history records.
						</p>
						<p>
							Deleting a history record does <strong>not</strong> stop or signal
							a running process.
						</p>
					</>
				}
			/>
			{deleteAllOpen && (
				<DeleteDialog
					isOpen
					onCancel={() => setDeleteAllOpen(false)}
					onConfirm={deleteAllMatching}
					confirmLoading={deleteMutation.isPending}
					entity="activity history"
					name="DELETE ALL"
					title="Delete all matching activity?"
					label="Type DELETE ALL to confirm"
					confirmText="Delete all matching"
					info={`This permanently deletes all ${deletableCount.toLocaleString()} records matching the current filters, including records on other pages. Running processes are not stopped or signaled.`}
				/>
			)}
		</>
	);
};

type ActivityHistoryTableProps = {
	rows: readonly ActivityDisplayRow[];
	realActivity: readonly WorkspaceCommandActivity[];
	selectedIds: ReadonlySet<string>;
	allCurrentPageSelected: boolean;
	sortBy: WorkspaceCommandActivitySort;
	sortDirection: WorkspaceCommandActivitySortDirection;
	idFilter: string;
	statuses: readonly ActivityDisplayStatus[];
	startedAfter: string;
	startedBefore: string;
	durationMin: string;
	durationMax: string;
	tools: readonly string[] | null;
	toolOptions: readonly ActivityFilterOption[];
	sources: readonly ActivityVisibleSource[];
	inputFilter: string;
	exitCode: string;
	onSort: (column: WorkspaceCommandActivitySort) => void;
	onID: (value: string) => void;
	onStatuses: (value: ActivityDisplayStatus[]) => void;
	onStarted: (after: string, before: string) => void;
	onDuration: (min: string, max: string) => void;
	onTools: (value: string[] | null) => void;
	onSources: (value: ActivityVisibleSource[]) => void;
	onInput: (value: string) => void;
	onExit: (value: string) => void;
	onSelectPage: (checked: boolean) => void;
	onSelect: (id: string, checked: boolean) => void;
};

const ActivityHistoryTable: FC<ActivityHistoryTableProps> = ({
	rows,
	realActivity,
	selectedIds,
	allCurrentPageSelected,
	sortBy,
	sortDirection,
	idFilter,
	statuses,
	startedAfter,
	startedBefore,
	durationMin,
	durationMax,
	tools,
	toolOptions,
	sources,
	inputFilter,
	exitCode,
	onSort,
	onID,
	onStatuses,
	onStarted,
	onDuration,
	onTools,
	onSources,
	onInput,
	onExit,
	onSelectPage,
	onSelect,
}) => (
	<Table aria-label="Activity history" className="table-fixed">
		<TableHeader>
			<TableRow>
				<TableHead className="w-36 px-2 text-content-primary">
					<div className="flex items-center gap-2">
						<Checkbox
							checked={allCurrentPageSelected}
							disabled={realActivity.length === 0}
							onCheckedChange={(checked) => onSelectPage(Boolean(checked))}
							aria-label="Select all activity on this page"
						/>
						<SortHeader
							label="ID"
							column="id"
							sortBy={sortBy}
							direction={sortDirection}
							onSort={onSort}
						/>
						<TextColumnFilter
							label="ID"
							value={idFilter}
							placeholder="Full ID or prefix"
							summary={idFilter ? "Filtered" : undefined}
							active={Boolean(idFilter)}
							onApply={onID}
						/>
					</div>
				</TableHead>
				<TableHead className="w-28 px-2 text-content-primary">
					<HeaderWithFilter
						label="Status"
						column="status"
						sortBy={sortBy}
						direction={sortDirection}
						onSort={onSort}
						filter={
							<MultiSelectColumnFilter
								label="Status"
								options={statusOptions}
								selected={statuses}
								searchPlaceholder="Search statuses..."
								summary={`${statuses.length}/${activityStatusOptions.length}`}
								active={!hasDefaultStatuses(statuses)}
								onApply={(value) =>
									onStatuses((value ?? []) as ActivityDisplayStatus[])
								}
							/>
						}
					/>
				</TableHead>
				<TableHead className="w-44 px-2 text-content-primary">
					<HeaderWithFilter
						label="Started"
						column="started"
						sortBy={sortBy}
						direction={sortDirection}
						onSort={onSort}
						filter={
							<RangeColumnFilter
								label="Started"
								firstLabel="From"
								secondLabel="To"
								firstValue={startedAfter}
								secondValue={startedBefore}
								inputType="datetime-local"
								summary={startedAfter || startedBefore ? "Filtered" : undefined}
								active={Boolean(startedAfter || startedBefore)}
								onApply={onStarted}
							/>
						}
					/>
				</TableHead>
				<TableHead className="w-32 px-2 text-content-primary">
					<HeaderWithFilter
						label="Duration"
						column="duration"
						sortBy={sortBy}
						direction={sortDirection}
						onSort={onSort}
						filter={
							<RangeColumnFilter
								label="Duration"
								firstLabel="Minimum"
								secondLabel="Maximum"
								firstValue={durationMin}
								secondValue={durationMax}
								firstPlaceholder="100ms, 1s, 2m, 1h"
								secondPlaceholder="100ms, 1s, 2m, 1h"
								summary={durationMin || durationMax ? "Filtered" : undefined}
								active={Boolean(durationMin || durationMax)}
								onApply={onDuration}
							/>
						}
					/>
				</TableHead>
				<TableHead className="w-32 px-2 text-content-primary">
					<HeaderWithFilter
						label="Tool"
						column="tool"
						sortBy={sortBy}
						direction={sortDirection}
						onSort={onSort}
						filter={
							<MultiSelectColumnFilter
								label="Tool"
								options={toolOptions}
								selected={tools}
								nullMeansAll
								searchPlaceholder="Search tools..."
								summary={
									tools === null
										? "All"
										: toolOptions.length > 0
											? `${tools.length}/${toolOptions.length}`
											: String(tools.length)
								}
								active={tools !== null}
								onApply={onTools}
							/>
						}
					/>
				</TableHead>
				<TableHead className="w-32 px-2 text-content-primary">
					<HeaderWithFilter
						label="Source"
						column="source"
						sortBy={sortBy}
						direction={sortDirection}
						onSort={onSort}
						filter={
							<MultiSelectColumnFilter
								label="Source"
								options={sourceFilterOptions}
								selected={sources}
								searchPlaceholder="Search sources..."
								summary={
									sources.length === activitySourceOptions.length
										? "All"
										: `${sources.length}/${activitySourceOptions.length}`
								}
								active={sources.length !== activitySourceOptions.length}
								onApply={(value) =>
									onSources((value ?? []) as ActivityVisibleSource[])
								}
							/>
						}
					/>
				</TableHead>
				<TableHead className="px-2 text-content-primary">
					<HeaderWithFilter
						label="Input"
						column="command"
						sortBy={sortBy}
						direction={sortDirection}
						onSort={onSort}
						filter={
							<TextColumnFilter
								label="Input"
								value={inputFilter}
								placeholder="Search input..."
								summary={inputFilter ? "Filtered" : undefined}
								active={Boolean(inputFilter)}
								onApply={onInput}
							/>
						}
					/>
				</TableHead>
				<TableHead className="w-20 px-2 text-content-primary">
					<HeaderWithFilter
						label="Exit"
						column="exit"
						sortBy={sortBy}
						direction={sortDirection}
						onSort={onSort}
						filter={
							<TextColumnFilter
								label="Exit"
								value={exitCode}
								placeholder="Exit code"
								inputType="number"
								summary={exitCode ? "Filtered" : undefined}
								active={Boolean(exitCode)}
								onApply={onExit}
							/>
						}
					/>
				</TableHead>
			</TableRow>
		</TableHeader>
		<TableBody>
			{rows.length === 0 ? (
				<TableRow>
					<TableCell colSpan={8}>
						<EmptyState message="No activity matches the current filters." />
					</TableCell>
				</TableRow>
			) : (
				rows.map((row) =>
					row.type === "idle" ? (
						<IdleActivityRow key={row.key} row={row} />
					) : (
						<RealActivityRow
							key={row.activity.id}
							item={row.activity}
							checked={selectedIds.has(row.activity.id)}
							onCheckedChange={(checked) => onSelect(row.activity.id, checked)}
						/>
					),
				)
			)}
		</TableBody>
	</Table>
);

const HeaderWithFilter: FC<{
	label: string;
	column: WorkspaceCommandActivitySort;
	sortBy: WorkspaceCommandActivitySort;
	direction: WorkspaceCommandActivitySortDirection;
	onSort: (column: WorkspaceCommandActivitySort) => void;
	filter: React.ReactNode;
}> = ({ label, column, sortBy, direction, onSort, filter }) => (
	<div className="flex min-w-0 items-center gap-1.5 overflow-hidden">
		<SortHeader
			label={label}
			column={column}
			sortBy={sortBy}
			direction={direction}
			onSort={onSort}
		/>
		{filter}
	</div>
);

const SortHeader: FC<{
	label: string;
	column: WorkspaceCommandActivitySort;
	sortBy: WorkspaceCommandActivitySort;
	direction: WorkspaceCommandActivitySortDirection;
	onSort: (column: WorkspaceCommandActivitySort) => void;
}> = ({ label, column, sortBy, direction, onSort }) => (
	<button
		type="button"
		className="inline-flex shrink-0 items-center gap-1 border-0 bg-transparent p-0 text-xs font-semibold text-content-primary hover:text-content-link"
		onClick={() => onSort(column)}
	>
		{label}
		<span aria-hidden>
			{sortBy === column ? (direction === "asc" ? "↑" : "↓") : "↕"}
		</span>
	</button>
);

const RealActivityRow: FC<{
	item: WorkspaceCommandActivity;
	checked: boolean;
	onCheckedChange: (checked: boolean) => void;
}> = ({ item, checked, onCheckedChange }) => (
	<TableRow data-state={checked ? "selected" : undefined}>
		<TableCell className="px-2">
			<div className="flex min-w-0 items-center gap-2">
				<Checkbox
					checked={checked}
					onCheckedChange={(value) => onCheckedChange(Boolean(value))}
					aria-label={`Select activity ${item.id}`}
				/>
				<CopyableValue
					value={item.id}
					className="font-mono text-2xs text-content-secondary hover:text-content-primary"
					title={item.id}
				>
					{shortActivityID(item.id)}
				</CopyableValue>
			</div>
		</TableCell>
		<TableCell className="px-2">
			<ActivityStatusBadge status={item.status} />
		</TableCell>
		<TableCell className="whitespace-nowrap px-2 text-xs">
			{formatTimestamp(item.started_at)}
		</TableCell>
		<TableCell className="whitespace-nowrap px-2">
			<ActivityDuration item={item} />
		</TableCell>
		<TableCell className="truncate px-2" title={item.tool || undefined}>
			{item.tool || "—"}
		</TableCell>
		<TableCell
			className="truncate px-2"
			title={activitySourceLabel(item.source)}
		>
			{activitySourceLabel(item.source)}
		</TableCell>
		<TableCell className="min-w-0 px-2">
			<code className="block whitespace-pre-wrap break-words text-xs text-content-primary">
				{activityInput(item) || "—"}
			</code>
			{item.work_dir && (
				<div
					className="mt-1 truncate text-2xs text-content-secondary"
					title={item.work_dir}
				>
					{item.work_dir}
				</div>
			)}
		</TableCell>
		<TableCell className="px-2">{item.exit_code ?? ""}</TableCell>
	</TableRow>
);

const IdleActivityRow: FC<{
	row: Extract<ActivityDisplayRow, { type: "idle" }>;
}> = ({ row }) => (
	<TableRow>
		<TableCell className="px-2" />
		<TableCell className="px-2">
			<ActivityStatusBadge status="idle" />
		</TableCell>
		<TableCell className="whitespace-nowrap px-2 text-xs">
			{formatTimestamp(row.startedAt)}
		</TableCell>
		<TableCell className="whitespace-nowrap px-2">
			<IdleDuration row={row} />
		</TableCell>
		<TableCell className="px-2" />
		<TableCell className="px-2" />
		<TableCell className="px-2" />
		<TableCell className="px-2" />
	</TableRow>
);

const IdleDuration: FC<{
	row: Extract<ActivityDisplayRow, { type: "idle" }>;
}> = ({ row }) => {
	const live = row.current && !row.finishedAt;
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!live) return;
		setNow(Date.now());
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, [live]);
	const started = new Date(row.startedAt).getTime();
	const finished = row.finishedAt ? new Date(row.finishedAt).getTime() : now;
	return <>{formatDurationMilliseconds(Math.max(0, finished - started))}</>;
};

const ActivityStatusBadge: FC<{ status: ActivityDisplayStatus }> = ({
	status,
}) => {
	const variant =
		status === "running"
			? "info"
			: status === "idle"
				? "default"
				: status === "succeeded"
					? "green"
					: status === "failed"
						? "destructive"
						: "warning";
	return (
		<Badge variant={variant} size="xs">
			{status}
		</Badge>
	);
};

const ActivityDuration: FC<{ item: WorkspaceCommandActivity }> = ({ item }) => {
	const live = !item.finished_at;
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!live) return;
		setNow(Date.now());
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, [live]);
	const started = new Date(item.started_at).getTime();
	const finished = item.finished_at
		? new Date(item.finished_at).getTime()
		: now;
	return <>{formatDurationMilliseconds(Math.max(0, finished - started))}</>;
};

const ActivityPagination: FC<{
	page: number;
	pageSize: number;
	totalCount: number;
	totalPages: number;
	onPage: (page: number) => void;
	onPageSize: (pageSize: number) => void;
}> = ({ page, pageSize, totalCount, totalPages, onPage, onPageSize }) => {
	const first = totalCount === 0 ? 0 : (page - 1) * pageSize + 1;
	const last = totalCount === 0 ? 0 : Math.min(page * pageSize, totalCount);
	return (
		<div className="mt-3 flex flex-wrap items-center gap-3 text-sm text-content-secondary">
			<span>
				{first}–{last} of {totalCount.toLocaleString()}
			</span>
			<div className="ml-auto flex flex-wrap items-center gap-3">
				<div className="flex items-center gap-2">
					<span>Rows per page</span>
					<Select
						value={String(pageSize)}
						onValueChange={(value) => onPageSize(Number(value))}
					>
						<SelectTrigger className="w-20">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							{pageSizeOptions.map((value) => (
								<SelectItem key={value} value={String(value)}>
									{value}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<PaginationWidgetBase
					currentPage={page}
					pageSize={pageSize}
					totalRecords={totalCount}
					totalPages={totalPages}
					onPageChange={onPage}
				/>
			</div>
		</div>
	);
};

const hasDefaultStatuses = (
	statuses: readonly ActivityDisplayStatus[],
): boolean =>
	statuses.length === defaultRealActivityStatuses.length &&
	defaultRealActivityStatuses.every((status) => statuses.includes(status));

const parseDurationFilter = (raw: string, label: string): ParsedDuration => {
	const value = raw.trim().toLowerCase();
	if (!value) return {};
	const match = value.match(/^(\d+(?:\.\d+)?)\s*(ms|s|m|h)?$/);
	if (!match) {
		return { error: `${label} must look like 100ms, 1s, 2m, or 1h.` };
	}
	const amount = Number(match[1]);
	const unit = match[2] ?? "ms";
	const multiplier =
		unit === "h" ? 3_600_000 : unit === "m" ? 60_000 : unit === "s" ? 1_000 : 1;
	return { value: Math.round(amount * multiplier) };
};

const parseExitCodeFilter = (raw: string): ParsedExitCode => {
	const value = raw.trim();
	if (!value) return {};
	if (!/^-?\d+$/.test(value)) return { error: "Exit code must be an integer." };
	const parsed = Number(value);
	if (
		!Number.isSafeInteger(parsed) ||
		parsed < -2_147_483_648 ||
		parsed > 2_147_483_647
	) {
		return { error: "Exit code must fit a 32-bit integer." };
	}
	return { value: parsed };
};

const toISOString = (localValue: string): string | undefined => {
	if (!localValue) return undefined;
	const value = new Date(localValue);
	return Number.isNaN(value.getTime()) ? undefined : value.toISOString();
};

const shortActivityID = (id: string): string =>
	`${id.replaceAll("-", "").slice(0, 10)}…`;

const formatTimestamp = (value?: string): string =>
	value ? dayjs(value).format("YYYY-MM-DD HH:mm:ss") : "";

const formatDurationMilliseconds = (milliseconds: number): string => {
	if (milliseconds < 1_000) return `${milliseconds} ms`;
	if (milliseconds < 60_000) return `${(milliseconds / 1_000).toFixed(1)} s`;
	const totalSeconds = Math.floor(milliseconds / 1_000);
	const hours = Math.floor(totalSeconds / 3_600);
	const minutes = Math.floor((totalSeconds % 3_600) / 60);
	const seconds = totalSeconds % 60;
	return hours > 0
		? `${hours}h ${minutes}m ${seconds}s`
		: `${minutes}m ${seconds}s`;
};

export default WorkspaceCommandActivityPage;
