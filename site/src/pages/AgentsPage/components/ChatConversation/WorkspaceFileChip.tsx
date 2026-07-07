import { HardDriveIcon } from "lucide-react";
import prettyBytes from "pretty-bytes";
import type { FC } from "react";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "#/components/Tooltip/Tooltip";

/**
 * Timeline chip for a workspace-file-reference message part. The
 * bytes live on the workspace filesystem, so unlike attachments there
 * is nothing to preview or download here; the tooltip surfaces the
 * absolute path the agent can read.
 */
export const WorkspaceFileChip: FC<{
	name: string;
	path: string;
	size: number;
}> = ({ name, path, size }) => (
	<Tooltip>
		<TooltipTrigger asChild>
			<div className="flex max-w-64 items-center gap-1.5 rounded-md border border-solid border-border-default bg-surface-tertiary px-2.5 py-1.5 text-xs">
				<HardDriveIcon
					aria-hidden="true"
					className="size-3.5 shrink-0 text-content-secondary"
				/>
				<span className="truncate font-medium text-content-primary">
					{name}
				</span>
				{size > 0 && (
					<span className="shrink-0 text-content-secondary">
						{prettyBytes(size)}
					</span>
				)}
			</div>
		</TooltipTrigger>
		<TooltipContent side="top">
			<p className="max-w-xs break-all font-mono text-xs">{path}</p>
		</TooltipContent>
	</Tooltip>
);
