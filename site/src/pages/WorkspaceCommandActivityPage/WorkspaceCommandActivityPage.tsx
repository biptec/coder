import dayjs from "dayjs";
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
	workspaceConnectionActivity,
} from "#/api/queries/workspaces";
import type {
	ConnectionType,
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
	WorkspaceConnectionActivityResponse,
	WorkspaceConnectionActivityType,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Badge } from "#/components/Badge/Badge";
import { Button } from "#/components/Button/Button";
import { Checkbox } from "#/components/Checkbox/Checkbox";
import { ConfirmDialog } from "#/components/Dialogs/ConfirmDialog/ConfirmDialog";
import { EmptyState } from "#/components/EmptyState/EmptyState";
import { Input } from "#/components/Input/Input";
import { Loader } from "#/components/Loader/Loader";
import { Margins } from "#/components/Margins/Margins";
import {
	PageHeader,
	PageHeaderSubtitle,
	PageHeaderTitle,
} from "#/components/PageHeader/PageHeader";
import {
	Popover,
	PopoverContent,
	PopoverTrigger,
} from "#/components/Popover/Popover";
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
import { pageTitle } from "#/utils/page";
import {
	defaultActivityStatuses as defaultStatusOptions,
	defaultWorkspaceActivityPreferences,
	loadWorkspaceActivityPreferences,
	activityPageSizeOptions as pageSizeOptions,
	saveWorkspaceActivityPreferences,
	activitySourceOptions as sourceOptions,
	activityStatusOptions as statusOptions,
} from "./activityPreferences";
import { updateCommandActivityCache } from "./commandActivityCache";

const connectionTypeLabels: Record<string, string> = {
	ssh: "SSH",
	reconnecting_pty: "Web terminal",
	vscode: "VS Code",
	jetbrains: "JetBrains",
	mcp: "MCP",
};

const commandSourceLabels: Record<WorkspaceCommandActivitySource, string> = {
	agentproc: "Agent (legacy)",
	mcp: "MCP",
	ssh: "SSH",
	reconnecting_pty: "Web terminal",
	vscode: "VS Code",
	jetbrains: "JetBrains",
	chat: "Coder Chat",
};

const statusLabel = (status: WorkspaceCommandActivityStatus): string =>
	status.charAt(0).toUpperCase() + status.slice(1);

type DeleteMode = "filtered" | "selected" | null;

type ParsedDuration = {
	value?: number;
	error?: string;
};

const WorkspaceCommandActivityPage: FC = () => {
	const params = useParams() as { username: string; workspace: string };
	const username = params.username.replace("@", "");
	const workspaceName = params.workspace;
	const workspaceQuery = useQuery(
		workspaceByOwnerAndName(username, workspaceName),
	);
	const workspaceId = workspaceQuery.data?.id;

	const [initialPreferences] = useState(loadWorkspaceActivityPreferences);
	const [search, setSearch] = useState(initialPreferences.search);
	const [debouncedSearch, setDebouncedSearch] = useState(
		initialPreferences.search.trim(),
	);
	const [statuses, setStatuses] = useState<WorkspaceCommandActivityStatus[]>(
		initialPreferences.statuses,
	);
	const [source, setSource] = useState<string>(initialPreferences.source);
	const [tools, setTools] = useState<string[] | null>(initialPreferences.tools);
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
	const [deleteMode, setDeleteMode] = useState<DeleteMode>(null);

	useEffect(() => {
		const timer = window.setTimeout(
			() => setDebouncedSearch(search.trim()),
			250,
		);
		return () => window.clearTimeout(timer);
	}, [search]);

	useEffect(() => {
		saveWorkspaceActivityPreferences({
			search,
			statuses,
			tools,
			source: source as "all" | WorkspaceCommandActivitySource,
			startedAfter,
			startedBefore,
			durationMin,
			durationMax,
			sortBy,
			sortDirection,
			pageSize,
		});
	}, [
		search,
		statuses,
		tools,
		source,
		startedAfter,
		startedBefore,
		durationMin,
		durationMax,
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
	const rangeError =
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
		rangeError ??
		startedRangeError;

	const filter = useMemo<WorkspaceCommandActivityFilter>(
		() => ({
			statuses,
			tools: tools ?? undefined,
			sources:
				source === "all"
					? undefined
					: [source as WorkspaceCommandActivitySource],
			search: debouncedSearch || undefined,
			started_after: toISOString(startedAfter),
			started_before: toISOString(startedBefore),
			duration_min_ms: parsedMinDuration.value,
			duration_max_ms: parsedMaxDuration.value,
		}),
		[
			statuses,
			tools,
			source,
			debouncedSearch,
			startedAfter,
			startedBefore,
			parsedMinDuration.value,
			parsedMaxDuration.value,
		],
	);

	const commandRequest = useMemo<WorkspaceCommandActivityRequest>(
		() => ({
			...filter,
			sort_by: sortBy,
			sort_direction: sortDirection,
			page,
			page_size: pageSize,
		}),
		[filter, sortBy, sortDirection, page, pageSize],
	);

	const hasToolSelection = tools === null || tools.length > 0;
	const commandQuery = useQuery({
		...workspaceCommandActivity(workspaceId, commandRequest),
		enabled:
			Boolean(workspaceId) &&
			!filterError &&
			statuses.length > 0 &&
			hasToolSelection,
	});
	const toolCatalogQuery = useQuery({
		...workspaceCommandActivity(workspaceId, {
			statuses: [...defaultStatusOptions],
			sort_by: "started",
			sort_direction: "desc",
			page: 1,
			page_size: 1,
		}),
		enabled:
			Boolean(workspaceId) &&
			!filterError &&
			(statuses.length === 0 || !hasToolSelection),
	});
	const connectionQuery = useQuery(workspaceConnectionActivity(workspaceId));
	const queryClient = useQueryClient();
	const deleteMutation = useMutation({
		...deleteWorkspaceCommandActivity(),
		onSuccess: (response) => {
			toast.success(
				`Cleared ${response.deleted} activity history ${response.deleted === 1 ? "record" : "records"}.`,
			);
			setSelectedIds(new Set());
			setDeleteMode(null);
			setPage(1);
			void queryClient.invalidateQueries({
				queryKey: ["workspaces", workspaceId, "command-activity"],
			});
		},
		onError: (error: unknown) => {
			toast.error(getErrorMessage(error, "Failed to clear activity history."));
		},
	});

	const hasActivitySelection = statuses.length > 0 && hasToolSelection;
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
	const activity = hasActivitySelection
		? (commandQuery.data?.activity ?? [])
		: [];
	const selectableActivity = useMemo(
		() => activity.filter((item) => item.status !== "idle"),
		[activity],
	);
	const currentPageIds = useMemo(
		() => new Set(selectableActivity.map((item) => item.id)),
		[selectableActivity],
	);
	const allCurrentPageSelected =
		selectableActivity.length > 0 &&
		selectableActivity.every((item) => selectedIds.has(item.id));
	const totalCount = hasActivitySelection
		? (commandQuery.data?.total_count ?? 0)
		: 0;
	const deletableCount = hasActivitySelection
		? (commandQuery.data?.deletable_count ?? 0)
		: 0;
	const totalPages = Math.max(
		1,
		hasActivitySelection ? (commandQuery.data?.total_pages ?? 0) : 0,
	);
	const hasActiveFilter = Boolean(
		!hasDefaultStatuses(statuses) ||
			tools !== null ||
			source !== "all" ||
			search.trim() ||
			startedAfter ||
			startedBefore ||
			durationMin.trim() ||
			durationMax.trim(),
	);

	useEffect(() => {
		if (page > totalPages) {
			setPage(totalPages);
		}
	}, [page, totalPages]);

	useEffect(() => {
		if (!workspaceId) return;

		let disposed = false;
		let reconnectTimer: number | undefined;
		let resyncTimer: number | undefined;
		let activeSocket: ReturnType<typeof watchWorkspaceActivity> | undefined;

		const commandKey = ["workspaces", workspaceId, "command-activity"] as const;
		const connectionKey = [
			"workspaces",
			workspaceId,
			"connection-activity",
		] as const;
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

		const connect = () => {
			if (disposed) return;
			const socket = watchWorkspaceActivity(workspaceId);
			activeSocket = socket;
			socket.addEventListener("message", (event) => {
				if (event.parseError || !event.parsedMessage) return;
				const envelope = event.parsedMessage as ServerSentEvent;
				if (envelope.type === "ping") {
					// The server sends ping only after the PubSub subscription is
					// installed. Resync once at this barrier so there is no race
					// between the initial REST snapshot and the realtime stream. The
					// same barrier restores any deltas missed during reconnect.
					void queryClient.invalidateQueries({ queryKey: commandKey });
					void queryClient.invalidateQueries({ queryKey: connectionKey });
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
					case "connection_update":
						if (payload.connection) {
							queryClient.setQueryData<WorkspaceConnectionActivityResponse>(
								connectionKey,
								payload.connection,
							);
						}
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
				// Do not leave an errored socket alive while the retry timer creates
				// another one. close() may also emit close; reconnect() is guarded.
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

	if (workspaceQuery.isLoading) {
		return <Loader />;
	}

	if (workspaceQuery.error) {
		return (
			<Margins className="py-8">
				<ErrorAlert error={workspaceQuery.error} />
			</Margins>
		);
	}

	const resetFilters = () => {
		const defaults = defaultWorkspaceActivityPreferences();
		setSearch(defaults.search);
		setDebouncedSearch(defaults.search);
		setStatuses(defaults.statuses);
		setTools(defaults.tools);
		setSource(defaults.source);
		setStartedAfter(defaults.startedAfter);
		setStartedBefore(defaults.startedBefore);
		setDurationMin(defaults.durationMin);
		setDurationMax(defaults.durationMax);
		setPage(1);
	};

	const updateSort = (column: WorkspaceCommandActivitySort) => {
		if (sortBy === column) {
			setSortDirection((current) => (current === "asc" ? "desc" : "asc"));
		} else {
			setSortBy(column);
			setSortDirection(column === "started" ? "desc" : "asc");
		}
		setPage(1);
	};

	const confirmDelete = () => {
		if (!workspaceId || !deleteMode) {
			return;
		}
		if (deleteMode === "selected") {
			deleteMutation.mutate({
				workspaceId,
				request: {
					mode: "selected",
					ids: Array.from(selectedIds),
				},
			});
			return;
		}
		deleteMutation.mutate({
			workspaceId,
			request: {
				mode: "filtered",
				filter,
			},
		});
	};

	return (
		<>
			<title>{pageTitle(`Workspace activity · ${workspaceName}`)}</title>
			<Margins className="pb-12">
				<PageHeader>
					<PageHeaderTitle>Workspace activity</PageHeaderTitle>
					<PageHeaderSubtitle>
						Live commands, MCP tools, and workspace connections for @{username}/
						{workspaceName}.
					</PageHeaderSubtitle>
				</PageHeader>

				<div className="space-y-8">
					<section className="space-y-3">
						<div className="flex items-baseline justify-between gap-4">
							<div>
								<h2 className="m-0 text-base font-semibold text-content-primary">
									Connections
								</h2>
								<p className="m-0 mt-1 text-sm text-content-secondary">
									Current MCP, SSH, terminal, VS Code, and JetBrains activity.
								</p>
							</div>
							{connectionQuery.data && (
								<Badge
									variant={connectionQuery.data.active ? "green" : "default"}
									size="sm"
								>
									{connectionQuery.data.active
										? `${connectionQuery.data.active_connections} active`
										: "No active connections"}
								</Badge>
							)}
						</div>

						{connectionQuery.error ? (
							<ErrorAlert error={connectionQuery.error} />
						) : connectionQuery.isLoading ? (
							<Loader />
						) : (
							<ConnectionActivityTable
								activity={connectionQuery.data?.types ?? []}
							/>
						)}
					</section>

					<section className="space-y-3">
						<div className="flex flex-wrap items-end justify-between gap-4">
							<div>
								<h2 className="m-0 text-base font-semibold text-content-primary">
									Activity history
								</h2>
								<p className="m-0 mt-1 text-sm text-content-secondary">
									Live command and MCP tool history. Filters, sorting,
									pagination, and clearing are applied to the full server-side
									history.
								</p>
							</div>
							<div className="flex flex-wrap items-center gap-2">
								{commandQuery.data?.history_limit ? (
									<Badge size="sm">
										Retention cap: {commandQuery.data.history_limit}
									</Badge>
								) : (
									<Badge size="sm">Unlimited history</Badge>
								)}
								<Badge size="sm">{totalCount} matching</Badge>
								{selectedIds.size > 0 && (
									<Badge size="sm">{selectedIds.size} selected</Badge>
								)}
								<Button
									size="sm"
									variant="destructive"
									disabled={selectedIds.size === 0 || deleteMutation.isPending}
									onClick={() => setDeleteMode("selected")}
								>
									Clear selected
								</Button>
								<Button
									size="sm"
									variant="destructive"
									disabled={
										deletableCount === 0 ||
										deleteMutation.isPending ||
										Boolean(filterError)
									}
									onClick={() => setDeleteMode("filtered")}
								>
									{hasActiveFilter ? "Clear all matching" : "Clear all"}
								</Button>
							</div>
						</div>

						<CommandActivityFilters
							search={search}
							source={source}
							startedAfter={startedAfter}
							startedBefore={startedBefore}
							durationMin={durationMin}
							durationMax={durationMax}
							error={filterError}
							onSearch={(value) => {
								setSearch(value);
								setPage(1);
							}}
							onSource={(value) => {
								setSource(value);
								setPage(1);
							}}
							onStartedAfter={(value) => {
								setStartedAfter(value);
								setPage(1);
							}}
							onStartedBefore={(value) => {
								setStartedBefore(value);
								setPage(1);
							}}
							onDurationMin={(value) => {
								setDurationMin(value);
								setPage(1);
							}}
							onDurationMax={(value) => {
								setDurationMax(value);
								setPage(1);
							}}
							onReset={resetFilters}
						/>

						{commandQuery.error ? (
							<ErrorAlert error={commandQuery.error} />
						) : filterError ? (
							<div className="rounded-md border border-border-default p-4 text-sm text-content-destructive">
								{filterError}
							</div>
						) : commandQuery.isLoading ? (
							<Loader />
						) : (
							<CommandActivityTable
								activity={activity}
								statuses={statuses}
								tools={tools}
								availableTools={availableTools}
								selectedIds={selectedIds}
								allCurrentPageSelected={allCurrentPageSelected}
								sortBy={sortBy}
								sortDirection={sortDirection}
								onSort={updateSort}
								onStatuses={(value) => {
									setStatuses(value);
									setSelectedIds(new Set());
									setPage(1);
								}}
								onTools={(value) => {
									setTools(value);
									setSelectedIds(new Set());
									setPage(1);
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

						<CommandActivityPagination
							page={page}
							pageSize={pageSize}
							totalCount={totalCount}
							totalPages={totalPages}
							onPage={setPage}
							onPageSize={(value) => {
								setPageSize(value);
								setPage(1);
							}}
						/>
					</section>
				</div>
			</Margins>

			<ConfirmDialog
				type="delete"
				open={deleteMode !== null}
				onClose={() => setDeleteMode(null)}
				onConfirm={confirmDelete}
				confirmLoading={deleteMutation.isPending}
				title={
					deleteMode === "selected"
						? `Clear ${selectedIds.size} selected history records?`
						: `Clear ${deletableCount} matching history records?`
				}
				confirmText="Clear history"
				description={
					<>
						<p>
							This permanently removes the matching workspace activity records
							from the database.
						</p>
						<p>
							Clearing history does <strong>not</strong> stop or signal a
							running process.
						</p>
					</>
				}
			/>
		</>
	);
};

type CommandActivityFiltersProps = {
	search: string;
	source: string;
	startedAfter: string;
	startedBefore: string;
	durationMin: string;
	durationMax: string;
	error?: string;
	onSearch: (value: string) => void;
	onSource: (value: string) => void;
	onStartedAfter: (value: string) => void;
	onStartedBefore: (value: string) => void;
	onDurationMin: (value: string) => void;
	onDurationMax: (value: string) => void;
	onReset: () => void;
};

const CommandActivityFilters: FC<CommandActivityFiltersProps> = ({
	search,
	source,
	startedAfter,
	startedBefore,
	durationMin,
	durationMax,
	error,
	onSearch,
	onSource,
	onStartedAfter,
	onStartedBefore,
	onDurationMin,
	onDurationMax,
	onReset,
}) => (
	<div className="rounded-lg border border-border-default p-4 space-y-4">
		<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
			<FilterField label="Command text">
				<Input
					value={search}
					onChange={(event) => onSearch(event.currentTarget.value)}
					placeholder="Live search command or arguments"
				/>
			</FilterField>
			<FilterField label="Source">
				<Select value={source} onValueChange={onSource}>
					<SelectTrigger>
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="all">All sources</SelectItem>
						{sourceOptions.map((value) => (
							<SelectItem key={value} value={value}>
								{commandSourceLabels[value]}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</FilterField>
		</div>

		<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
			<FilterField label="Started from">
				<Input
					type="datetime-local"
					value={startedAfter}
					onChange={(event) => onStartedAfter(event.currentTarget.value)}
				/>
			</FilterField>
			<FilterField label="Started to">
				<Input
					type="datetime-local"
					value={startedBefore}
					onChange={(event) => onStartedBefore(event.currentTarget.value)}
				/>
			</FilterField>
			<FilterField label="Duration min">
				<Input
					value={durationMin}
					onChange={(event) => onDurationMin(event.currentTarget.value)}
					placeholder="100ms, 1s, 2m, 1h"
					aria-invalid={Boolean(error)}
				/>
			</FilterField>
			<FilterField label="Duration max">
				<Input
					value={durationMax}
					onChange={(event) => onDurationMax(event.currentTarget.value)}
					placeholder="100ms, 1s, 2m, 1h"
					aria-invalid={Boolean(error)}
				/>
			</FilterField>
		</div>

		<div className="flex items-center justify-between gap-4">
			<div className="text-xs text-content-secondary">
				Search is debounced by 250 ms. Live updates stream over WebSocket; there
				is no periodic history polling.
			</div>
			<Button size="sm" variant="subtle" onClick={onReset}>
				Reset filters
			</Button>
		</div>
	</div>
);

const StatusMultiSelect: FC<{
	statuses: readonly WorkspaceCommandActivityStatus[];
	onChange: (statuses: WorkspaceCommandActivityStatus[]) => void;
	compact?: boolean;
}> = ({ statuses, onChange, compact = false }) => {
	const triggerLabel = compact
		? `${statuses.length}/${statusOptions.length}`
		: statuses.length === 0
			? "No statuses"
			: statuses.length === statusOptions.length
				? "All statuses"
				: statuses.length === 1
					? statusLabel(statuses[0])
					: `${statuses.length} statuses`;

	const toggle = (status: WorkspaceCommandActivityStatus, checked: boolean) => {
		const next = new Set(statuses);
		if (checked) next.add(status);
		else next.delete(status);
		onChange(statusOptions.filter((candidate) => next.has(candidate)));
	};

	return (
		<Popover>
			<PopoverTrigger asChild>
				<Button
					variant={compact ? "subtle" : "outline"}
					size={compact ? "xs" : undefined}
					className={
						compact
							? "min-w-0 px-1 font-normal"
							: "w-full justify-between font-normal"
					}
					aria-label="Filter activity statuses"
				>
					<span>{triggerLabel}</span>
					<span aria-hidden="true" className="text-content-secondary">
						▾
					</span>
				</Button>
			</PopoverTrigger>
			<PopoverContent align="start" className="w-64 p-2">
				<div className="space-y-1" role="group" aria-label="Command statuses">
					{statusOptions.map((status) => {
						const checked = statuses.includes(status);
						const id = `command-status-${status}`;
						return (
							<div
								key={status}
								className="flex items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-surface-secondary"
							>
								<Checkbox
									id={id}
									checked={checked}
									onCheckedChange={(value) => toggle(status, Boolean(value))}
								/>
								<label htmlFor={id} className="flex-1 cursor-pointer">
									{statusLabel(status)}
								</label>
							</div>
						);
					})}
				</div>
			</PopoverContent>
		</Popover>
	);
};

const ToolMultiSelect: FC<{
	tools: readonly string[] | null;
	availableTools: readonly string[];
	onChange: (tools: string[] | null) => void;
	compact?: boolean;
}> = ({ tools, availableTools, onChange, compact = false }) => {
	const triggerLabel = compact
		? tools === null
			? "All"
			: availableTools.length > 0
				? `${tools.length}/${availableTools.length}`
				: String(tools.length)
		: tools === null
			? "All tools"
			: tools.length === 0
				? "No tools"
				: tools.length === 1
					? tools[0]
					: `${tools.length} tools`;

	const toggle = (tool: string, checked: boolean) => {
		const next = new Set(tools ?? availableTools);
		if (checked) next.add(tool);
		else next.delete(tool);
		const ordered = availableTools.filter((candidate) => next.has(candidate));
		const allSelected =
			availableTools.length > 0 && ordered.length === availableTools.length;
		onChange(allSelected ? null : ordered);
	};

	return (
		<Popover>
			<PopoverTrigger asChild>
				<Button
					variant={compact ? "subtle" : "outline"}
					size={compact ? "xs" : undefined}
					className={
						compact
							? "min-w-0 px-1 font-normal"
							: "w-full justify-between font-normal"
					}
					aria-label="Filter activity tools"
				>
					<span className="truncate">{triggerLabel}</span>
					<span aria-hidden="true" className="text-content-secondary">
						▾
					</span>
				</Button>
			</PopoverTrigger>
			<PopoverContent align="start" className="w-72 p-2">
				<div
					className="max-h-80 space-y-1 overflow-y-auto"
					role="group"
					aria-label="MCP tools"
				>
					{availableTools.length === 0 ? (
						<div className="px-2 py-2 text-sm text-content-secondary">
							No tools available yet.
						</div>
					) : (
						availableTools.map((tool) => {
							const checked = tools === null || tools.includes(tool);
							const id = `activity-tool-${tool}`;
							return (
								<div
									key={tool}
									className="flex items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-surface-secondary"
								>
									<Checkbox
										id={id}
										checked={checked}
										onCheckedChange={(value) => toggle(tool, Boolean(value))}
									/>
									<label
										htmlFor={id}
										className="min-w-0 flex-1 cursor-pointer truncate"
										title={tool}
									>
										{tool}
									</label>
								</div>
							);
						})
					)}
				</div>
			</PopoverContent>
		</Popover>
	);
};

const FilterField: FC<{ label: string; children: React.ReactNode }> = ({
	label,
	children,
}) => (
	<fieldset className="m-0 min-w-0 space-y-1 border-0 p-0 text-xs font-medium text-content-secondary">
		<legend className="mb-1 block p-0">{label}</legend>
		{children}
	</fieldset>
);

type ConnectionActivityTableProps = {
	activity: readonly WorkspaceConnectionActivityType[];
};

const ConnectionActivityTable: FC<ConnectionActivityTableProps> = ({
	activity,
}) => {
	return (
		<Table aria-label="Workspace connection activity">
			<TableHeader>
				<TableRow>
					<TableHead>Type</TableHead>
					<TableHead>Status</TableHead>
					<TableHead>Last connected</TableHead>
					<TableHead>Last disconnected</TableHead>
					<TableHead>Last activity</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{activity.map((item) => (
					<ConnectionActivityRow key={item.type} item={item} />
				))}
			</TableBody>
		</Table>
	);
};

const ConnectionActivityRow: FC<{ item: WorkspaceConnectionActivityType }> = ({
	item,
}) => {
	return (
		<TableRow>
			<TableCell className="font-medium text-content-primary">
				{connectionTypeLabel(item.type)}
			</TableCell>
			<TableCell>
				<Badge
					variant={item.active_connections > 0 ? "green" : "default"}
					size="xs"
				>
					{item.active_connections > 0
						? `${item.active_connections} active`
						: "Idle"}
				</Badge>
			</TableCell>
			<TableCell>{formatTimestamp(item.last_connected_at)}</TableCell>
			<TableCell>{formatTimestamp(item.last_disconnected_at)}</TableCell>
			<TableCell>{formatTimestamp(item.last_activity_at)}</TableCell>
		</TableRow>
	);
};

type CommandActivityTableProps = {
	activity: readonly WorkspaceCommandActivity[];
	statuses: readonly WorkspaceCommandActivityStatus[];
	tools: readonly string[] | null;
	availableTools: readonly string[];
	selectedIds: ReadonlySet<string>;
	allCurrentPageSelected: boolean;
	sortBy: WorkspaceCommandActivitySort;
	sortDirection: WorkspaceCommandActivitySortDirection;
	onSort: (column: WorkspaceCommandActivitySort) => void;
	onStatuses: (statuses: WorkspaceCommandActivityStatus[]) => void;
	onTools: (tools: string[] | null) => void;
	onSelectPage: (checked: boolean) => void;
	onSelect: (id: string, checked: boolean) => void;
};

const CommandActivityTable: FC<CommandActivityTableProps> = ({
	activity,
	statuses,
	tools,
	availableTools,
	selectedIds,
	allCurrentPageSelected,
	sortBy,
	sortDirection,
	onSort,
	onStatuses,
	onTools,
	onSelectPage,
	onSelect,
}) => {
	return (
		<div className="overflow-x-auto">
			<Table aria-label="Workspace activity history">
				<TableHeader>
					<TableRow>
						<TableHead className="min-w-52">
							<div className="flex items-center gap-3">
								<Checkbox
									checked={allCurrentPageSelected}
									disabled={!activity.some((item) => item.status !== "idle")}
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
							</div>
						</TableHead>
						<TableHead>
							<div className="flex items-center gap-1">
								<SortHeader
									label="Status"
									column="status"
									sortBy={sortBy}
									direction={sortDirection}
									onSort={onSort}
								/>
								<StatusMultiSelect
									statuses={statuses}
									onChange={onStatuses}
									compact
								/>
							</div>
						</TableHead>
						<SortableHead
							label="Started"
							column="started"
							{...{ sortBy, sortDirection, onSort }}
						/>
						<SortableHead
							label="Duration"
							column="duration"
							{...{ sortBy, sortDirection, onSort }}
						/>
						<TableHead>
							<div className="flex items-center gap-1">
								<SortHeader
									label="Tool"
									column="tool"
									sortBy={sortBy}
									direction={sortDirection}
									onSort={onSort}
								/>
								<ToolMultiSelect
									tools={tools}
									availableTools={availableTools}
									onChange={onTools}
									compact
								/>
							</div>
						</TableHead>
						<SortableHead
							label="Source"
							column="source"
							{...{ sortBy, sortDirection, onSort }}
						/>
						<SortableHead
							label="Command"
							column="command"
							{...{ sortBy, sortDirection, onSort }}
						/>
						<SortableHead
							label="Exit"
							column="exit"
							{...{ sortBy, sortDirection, onSort }}
						/>
					</TableRow>
				</TableHeader>
				<TableBody>
					{activity.length === 0 ? (
						<TableRow>
							<TableCell colSpan={8}>
								<EmptyState message="No workspace activity matches the current filters." />
							</TableCell>
						</TableRow>
					) : (
						activity.map((item) => (
							<CommandActivityRow
								key={item.id}
								item={item}
								checked={selectedIds.has(item.id)}
								onCheckedChange={(checked) => onSelect(item.id, checked)}
							/>
						))
					)}
				</TableBody>
			</Table>
		</div>
	);
};

type SortableHeadProps = {
	label: string;
	column: WorkspaceCommandActivitySort;
	sortBy: WorkspaceCommandActivitySort;
	sortDirection: WorkspaceCommandActivitySortDirection;
	onSort: (column: WorkspaceCommandActivitySort) => void;
};

const SortableHead: FC<SortableHeadProps> = ({
	label,
	column,
	sortBy,
	sortDirection,
	onSort,
}) => (
	<TableHead>
		<SortHeader
			label={label}
			column={column}
			sortBy={sortBy}
			direction={sortDirection}
			onSort={onSort}
		/>
	</TableHead>
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
		className="inline-flex items-center gap-1 font-medium text-content-secondary hover:text-content-primary"
		onClick={() => onSort(column)}
	>
		{label}
		<span aria-hidden>
			{sortBy === column ? (direction === "asc" ? "↑" : "↓") : "↕"}
		</span>
	</button>
);

const CommandActivityRow: FC<{
	item: WorkspaceCommandActivity;
	checked: boolean;
	onCheckedChange: (checked: boolean) => void;
}> = ({ item, checked, onCheckedChange }) => {
	const idle = item.status === "idle";
	return (
		<TableRow>
			<TableCell>
				{!idle && (
					<div className="flex items-center gap-3">
						<Checkbox
							checked={checked}
							onCheckedChange={(value) => onCheckedChange(Boolean(value))}
							aria-label={`Select activity ${item.id}`}
						/>
						<code className="text-2xs text-content-secondary" title={item.id}>
							{item.id}
						</code>
					</div>
				)}
			</TableCell>
			<TableCell>
				<CommandStatusBadge status={item.status} />
			</TableCell>
			<TableCell className="whitespace-nowrap">
				{formatTimestamp(item.started_at)}
			</TableCell>
			<TableCell className="whitespace-nowrap">
				<CommandDuration item={item} />
			</TableCell>
			<TableCell>{idle ? "" : item.tool || "—"}</TableCell>
			<TableCell>
				{idle ? "" : (commandSourceLabels[item.source] ?? item.source)}
			</TableCell>
			<TableCell className="min-w-72 max-w-[48rem]">
				{!idle && (
					<>
						<code className="block whitespace-pre-wrap break-words text-xs text-content-primary">
							{displayCommand(item)}
						</code>
						{item.work_dir && (
							<div
								className="mt-1 truncate text-2xs text-content-secondary"
								title={item.work_dir}
							>
								{item.work_dir}
							</div>
						)}
					</>
				)}
			</TableCell>
			<TableCell>{idle ? "" : (item.exit_code ?? "")}</TableCell>
		</TableRow>
	);
};

const CommandStatusBadge: FC<{ status: WorkspaceCommandActivityStatus }> = ({
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

type CommandActivityPaginationProps = {
	page: number;
	pageSize: number;
	totalCount: number;
	totalPages: number;
	onPage: (page: number) => void;
	onPageSize: (pageSize: number) => void;
};

const CommandActivityPagination: FC<CommandActivityPaginationProps> = ({
	page,
	pageSize,
	totalCount,
	totalPages,
	onPage,
	onPageSize,
}) => {
	const first = totalCount === 0 ? 0 : (page - 1) * pageSize + 1;
	const last = totalCount === 0 ? 0 : Math.min(page * pageSize, totalCount);
	return (
		<div className="flex flex-wrap items-center justify-between gap-3 text-sm text-content-secondary">
			<div>
				{first}–{last} of {totalCount}
			</div>
			<div className="flex items-center gap-2">
				<span>Rows per page</span>
				<Select
					value={String(pageSize)}
					onValueChange={(value) => onPageSize(Number(value))}
				>
					<SelectTrigger className="w-24">
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
				<Button
					size="sm"
					variant="subtle"
					disabled={page <= 1}
					onClick={() => onPage(1)}
				>
					«
				</Button>
				<Button
					size="sm"
					variant="subtle"
					disabled={page <= 1}
					onClick={() => onPage(page - 1)}
				>
					‹
				</Button>
				<span className="min-w-28 text-center">
					Page {page} of {totalPages}
				</span>
				<Button
					size="sm"
					variant="subtle"
					disabled={page >= totalPages}
					onClick={() => onPage(page + 1)}
				>
					›
				</Button>
				<Button
					size="sm"
					variant="subtle"
					disabled={page >= totalPages}
					onClick={() => onPage(totalPages)}
				>
					»
				</Button>
			</div>
		</div>
	);
};

const hasDefaultStatuses = (
	statuses: readonly WorkspaceCommandActivityStatus[],
): boolean =>
	statuses.length === defaultStatusOptions.length &&
	defaultStatusOptions.every((status) => statuses.includes(status));

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

const toISOString = (localValue: string): string | undefined => {
	if (!localValue) return undefined;
	const value = new Date(localValue);
	return Number.isNaN(value.getTime()) ? undefined : value.toISOString();
};

const connectionTypeLabel = (type: ConnectionType): string =>
	connectionTypeLabels[type] ?? type;

const displayCommand = (item: WorkspaceCommandActivity): string => {
	if (item.kind === "tool") return "";
	if (item.command) {
		return item.command;
	}
	if (item.argv && item.argv.length > 0) {
		return item.argv.join(" ");
	}
	return "Unknown command";
};

const formatTimestamp = (value?: string): string => {
	if (!value) {
		return "Never";
	}
	return dayjs(value).format("YYYY-MM-DD HH:mm:ss");
};

const CommandDuration: FC<{ item: WorkspaceCommandActivity }> = ({ item }) => {
	const live = !item.finished_at;
	const [now, setNow] = useState(() => Date.now());

	useEffect(() => {
		if (!live) return;
		setNow(Date.now());
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, [live]);

	return <>{formatDuration(item, now)}</>;
};

const formatDuration = (
	item: WorkspaceCommandActivity,
	now: number,
): string => {
	const started = dayjs(item.started_at);
	const finished = item.finished_at ? dayjs(item.finished_at) : dayjs(now);
	const milliseconds = Math.max(0, finished.diff(started));

	if (milliseconds < 1_000) {
		return `${milliseconds} ms`;
	}
	if (milliseconds < 60_000) {
		return `${(milliseconds / 1_000).toFixed(1)} s`;
	}

	const totalSeconds = Math.floor(milliseconds / 1_000);
	const hours = Math.floor(totalSeconds / 3_600);
	const minutes = Math.floor((totalSeconds % 3_600) / 60);
	const seconds = totalSeconds % 60;
	return hours > 0
		? `${hours}h ${minutes}m ${seconds}s`
		: `${minutes}m ${seconds}s`;
};

export default WorkspaceCommandActivityPage;
