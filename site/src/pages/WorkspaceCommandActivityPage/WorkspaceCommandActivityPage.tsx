import dayjs from "dayjs";
import { type FC, useMemo } from "react";
import { useQuery } from "react-query";
import { useParams } from "react-router";
import {
	workspaceByOwnerAndName,
	workspaceCommandActivity,
	workspaceConnectionActivity,
} from "#/api/queries/workspaces";
import type {
	ConnectionType,
	WorkspaceCommandActivity,
	WorkspaceCommandActivityStatus,
	WorkspaceConnectionActivityType,
} from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Badge } from "#/components/Badge/Badge";
import { EmptyState } from "#/components/EmptyState/EmptyState";
import { Loader } from "#/components/Loader/Loader";
import { Margins } from "#/components/Margins/Margins";
import {
	PageHeader,
	PageHeaderSubtitle,
	PageHeaderTitle,
} from "#/components/PageHeader/PageHeader";
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
};

const WorkspaceCommandActivityPage: FC = () => {
	const params = useParams() as { username: string; workspace: string };
	const username = params.username.replace("@", "");
	const workspaceName = params.workspace;
	const workspaceQuery = useQuery(
		workspaceByOwnerAndName(username, workspaceName),
	);
	const workspaceId = workspaceQuery.data?.id;
	const commandQuery = useQuery(workspaceCommandActivity(workspaceId));
	const connectionQuery = useQuery(workspaceConnectionActivity(workspaceId));

	const runningCommands = useMemo(
		() =>
			commandQuery.data?.activity.filter((item) => item.status === "running") ??
			[],
		[commandQuery.data],
	);

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
									Current SSH, terminal, VS Code, and JetBrains activity.
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
						<div className="flex items-baseline justify-between gap-4">
							<div>
								<h2 className="m-0 text-base font-semibold text-content-primary">
									Commands
								</h2>
								<p className="m-0 mt-1 text-sm text-content-secondary">
									Running commands and the most recent command history retained
									by Coder.
								</p>
							</div>
							{commandQuery.data && (
								<div className="flex gap-2">
									{runningCommands.length > 0 && (
										<Badge variant="info" size="sm">
											{runningCommands.length} running
										</Badge>
									)}
									<Badge size="sm">
										History limit: {commandQuery.data.history_limit}
									</Badge>
								</div>
							)}
						</div>

						{commandQuery.error ? (
							<ErrorAlert error={commandQuery.error} />
						) : commandQuery.isLoading ? (
							<Loader />
						) : (
							<CommandActivityTable
								activity={commandQuery.data?.activity ?? []}
							/>
						)}
					</section>
				</div>
			</Margins>
		</>
	);
};

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
};

const CommandActivityTable: FC<CommandActivityTableProps> = ({ activity }) => {
	if (activity.length === 0) {
		return <EmptyState message="No command activity has been recorded yet." />;
	}

	return (
		<Table aria-label="Workspace command activity">
			<TableHeader>
				<TableRow>
					<TableHead>Status</TableHead>
					<TableHead>Started</TableHead>
					<TableHead>Duration</TableHead>
					<TableHead>Tool</TableHead>
					<TableHead>Source</TableHead>
					<TableHead>Command</TableHead>
					<TableHead>Exit</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{activity.map((item) => (
					<CommandActivityRow key={item.id} item={item} />
				))}
			</TableBody>
		</Table>
	);
};

const CommandActivityRow: FC<{ item: WorkspaceCommandActivity }> = ({
	item,
}) => {
	return (
		<TableRow>
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
			<TableCell>{item.source === "agentproc" ? "Agent" : "SSH"}</TableCell>
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
