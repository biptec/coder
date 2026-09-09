import dayjs from "dayjs";
import { type FC, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { useParams } from "react-router";
import { toast } from "sonner";
import { getErrorMessage } from "#/api/errors";
import {
	deleteWorkspaceCommandActivity,
	workspaceByOwnerAndName,
	workspaceCommandActivity,
	workspaceConnectionActivity,
} from "#/api/queries/workspaces";
import type {
	ConnectionType,
	WorkspaceCommandActivity,
	WorkspaceCommandActivityFilter,
	WorkspaceCommandActivityRequest,
	WorkspaceCommandActivitySort,
	WorkspaceCommandActivitySortDirection,
	WorkspaceCommandActivitySource,
	WorkspaceCommandActivityStatus,
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

const sourceOptions: readonly WorkspaceCommandActivitySource[] = [
	"mcp",
	"ssh",
	"reconnecting_pty",
	"vscode",
	"jetbrains",
	"chat",
	"agentproc",
];

const statusOptions: readonly WorkspaceCommandActivityStatus[] = [
	"running",
	"succeeded",
	"failed",
	"interrupted",
];

const pageSizeOptions = [25, 50, 100, 250, 500] as const;

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

	const [search, setSearch] = useState("");
	const [debouncedSearch, setDebouncedSearch] = useState("");
	const [status, setStatus] = useState<string>("all");
	const [source, setSource] = useState<string>("all");
	const [tool, setTool] = useState("");
	const [startedAfter, setStartedAfter] = useState("");
	const [startedBefore, setStartedBefore] = useState("");
	const [durationMin, setDurationMin] = useState("");
	const [durationMax, setDurationMax] = useState("");
	const [sortBy, setSortBy] = useState<WorkspaceCommandActivitySort>("started");
	const [sortDirection, setSortDirection] =
		useState<WorkspaceCommandActivitySortDirection>("desc");
	const [page, setPage] = useState(1);
	const [pageSize, setPageSize] = useState(50);
	const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
	const [deleteMode, setDeleteMode] = useState<DeleteMode>(null);

	useEffect(() => {
		const timer = window.setTimeout(
			() => setDebouncedSearch(search.trim()),
			250,
		);
		return () => window.clearTimeout(timer);
	}, [search]);

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
			statuses:
				status === "all"
					? undefined
					: [status as WorkspaceCommandActivityStatus],
			tools: tool.trim() ? [tool.trim()] : undefined,
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
			status,
			tool,
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

	const commandQuery = useQuery({
		...workspaceCommandActivity(workspaceId, commandRequest),
		enabled: Boolean(workspaceId) && !filterError,
	});
	const connectionQuery = useQuery(workspaceConnectionActivity(workspaceId));
	const queryClient = useQueryClient();
	const deleteMutation = useMutation({
		...deleteWorkspaceCommandActivity(),
		onSuccess: (response) => {
			toast.success(
				`Cleared ${response.deleted} command history ${response.deleted === 1 ? "record" : "records"}.`,
			);
			setSelectedIds(new Set());
			setDeleteMode(null);
			setPage(1);
			void queryClient.invalidateQueries({
				queryKey: ["workspaces", workspaceId, "command-activity"],
			});
		},
		onError: (error: unknown) => {
			toast.error(getErrorMessage(error, "Failed to clear command history."));
		},
	});

	const activity = commandQuery.data?.activity ?? [];
	const currentPageIds = useMemo(
		() => new Set(activity.map((item) => item.id)),
		[activity],
	);
	const allCurrentPageSelected =
		activity.length > 0 && activity.every((item) => selectedIds.has(item.id));
	const totalCount = commandQuery.data?.total_count ?? 0;
	const totalPages = Math.max(1, commandQuery.data?.total_pages ?? 0);
	const hasActiveFilter = Boolean(
		status !== "all" ||
			source !== "all" ||
			tool.trim() ||
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
		setSearch("");
		setDebouncedSearch("");
		setStatus("all");
		setSource("all");
		setTool("");
		setStartedAfter("");
		setStartedBefore("");
		setDurationMin("");
		setDurationMax("");
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
			<title>{pageTitle(`Command activity · ${workspaceName}`)}</title>
			<Margins className="pb-12">
				<PageHeader>
					<PageHeaderTitle>Command activity</PageHeaderTitle>
					<PageHeaderSubtitle>
						Live commands and workspace connections for @{username}/
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
									Commands
								</h2>
								<p className="m-0 mt-1 text-sm text-content-secondary">
									Live command history. Filters, sorting, pagination, and
									clearing are applied to the full server-side history.
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
										totalCount === 0 ||
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
							status={status}
							source={source}
							tool={tool}
							startedAfter={startedAfter}
							startedBefore={startedBefore}
							durationMin={durationMin}
							durationMax={durationMax}
							error={filterError}
							onSearch={(value) => {
								setSearch(value);
								setPage(1);
							}}
							onStatus={(value) => {
								setStatus(value);
								setPage(1);
							}}
							onSource={(value) => {
								setSource(value);
								setPage(1);
							}}
							onTool={(value) => {
								setTool(value);
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
								selectedIds={selectedIds}
								allCurrentPageSelected={allCurrentPageSelected}
								sortBy={sortBy}
								sortDirection={sortDirection}
								onSort={updateSort}
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
						: `Clear ${totalCount} matching history records?`
				}
				confirmText="Clear history"
				description={
					<>
						<p>
							This permanently removes the matching Command Activity records
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
	status: string;
	source: string;
	tool: string;
	startedAfter: string;
	startedBefore: string;
	durationMin: string;
	durationMax: string;
	error?: string;
	onSearch: (value: string) => void;
	onStatus: (value: string) => void;
	onSource: (value: string) => void;
	onTool: (value: string) => void;
	onStartedAfter: (value: string) => void;
	onStartedBefore: (value: string) => void;
	onDurationMin: (value: string) => void;
	onDurationMax: (value: string) => void;
	onReset: () => void;
};

const CommandActivityFilters: FC<CommandActivityFiltersProps> = ({
	search,
	status,
	source,
	tool,
	startedAfter,
	startedBefore,
	durationMin,
	durationMax,
	error,
	onSearch,
	onStatus,
	onSource,
	onTool,
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
			<FilterField label="Status">
				<Select value={status} onValueChange={onStatus}>
					<SelectTrigger>
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="all">All statuses</SelectItem>
						{statusOptions.map((value) => (
							<SelectItem key={value} value={value}>
								{value}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</FilterField>
			<FilterField label="Tool">
				<Input
					value={tool}
					onChange={(event) => onTool(event.currentTarget.value)}
					placeholder="Exact tool, e.g. exec"
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
				Search is debounced by 250 ms. History refreshes live every second.
			</div>
			<Button size="sm" variant="subtle" onClick={onReset}>
				Reset filters
			</Button>
		</div>
	</div>
);

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
	selectedIds: ReadonlySet<string>;
	allCurrentPageSelected: boolean;
	sortBy: WorkspaceCommandActivitySort;
	sortDirection: WorkspaceCommandActivitySortDirection;
	onSort: (column: WorkspaceCommandActivitySort) => void;
	onSelectPage: (checked: boolean) => void;
	onSelect: (id: string, checked: boolean) => void;
};

const CommandActivityTable: FC<CommandActivityTableProps> = ({
	activity,
	selectedIds,
	allCurrentPageSelected,
	sortBy,
	sortDirection,
	onSort,
	onSelectPage,
	onSelect,
}) => {
	if (activity.length === 0) {
		return (
			<EmptyState message="No command activity matches the current filters." />
		);
	}

	return (
		<div className="overflow-x-auto">
			<Table aria-label="Workspace command activity">
				<TableHeader>
					<TableRow>
						<TableHead className="min-w-52">
							<div className="flex items-center gap-3">
								<Checkbox
									checked={allCurrentPageSelected}
									onCheckedChange={(checked) => onSelectPage(Boolean(checked))}
									aria-label="Select all commands on this page"
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
						<SortableHead
							label="Status"
							column="status"
							{...{ sortBy, sortDirection, onSort }}
						/>
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
						<SortableHead
							label="Tool"
							column="tool"
							{...{ sortBy, sortDirection, onSort }}
						/>
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
					{activity.map((item) => (
						<CommandActivityRow
							key={item.id}
							item={item}
							checked={selectedIds.has(item.id)}
							onCheckedChange={(checked) => onSelect(item.id, checked)}
						/>
					))}
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
	return (
		<TableRow>
			<TableCell>
				<div className="flex items-center gap-3">
					<Checkbox
						checked={checked}
						onCheckedChange={(value) => onCheckedChange(Boolean(value))}
						aria-label={`Select command ${item.id}`}
					/>
					<code className="text-2xs text-content-secondary" title={item.id}>
						{item.id}
					</code>
				</div>
			</TableCell>
			<TableCell>
				<CommandStatusBadge status={item.status} />
			</TableCell>
			<TableCell className="whitespace-nowrap">
				{formatTimestamp(item.started_at)}
			</TableCell>
			<TableCell className="whitespace-nowrap">
				{formatDuration(item)}
			</TableCell>
			<TableCell>{item.tool || "—"}</TableCell>
			<TableCell>{commandSourceLabels[item.source] ?? item.source}</TableCell>
			<TableCell className="min-w-72 max-w-[48rem]">
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
			</TableCell>
			<TableCell>{item.exit_code ?? ""}</TableCell>
		</TableRow>
	);
};

const CommandStatusBadge: FC<{ status: WorkspaceCommandActivityStatus }> = ({
	status,
}) => {
	const variant =
		status === "running"
			? "info"
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

const formatDuration = (item: WorkspaceCommandActivity): string => {
	const started = dayjs(item.started_at);
	const finished = item.finished_at ? dayjs(item.finished_at) : dayjs();
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
