import { AlertCircleIcon, CheckCircle2Icon } from "lucide-react";
import type { FC } from "react";
import type {
	WorkspaceVolumeCopyOperation,
	WorkspaceVolumeCopyVolume,
} from "#/api/typesGenerated";
import { Alert } from "#/components/Alert/Alert";
import { Checkbox } from "#/components/Checkbox/Checkbox";

export type VolumeChoice = {
	copy: boolean;
	overwrite: boolean;
};

export type VolumeRow = {
	source: WorkspaceVolumeCopyVolume;
	destination?: WorkspaceVolumeCopyVolume;
};

export const VolumeSelectionList: FC<{
	rows: VolumeRow[];
	choices: Record<string, VolumeChoice>;
	disabled: boolean;
	onChange: (key: string, choice: VolumeChoice) => void;
}> = ({ rows, choices, disabled, onChange }) => {
	return (
		<div className="rounded-lg border border-border-default divide-y divide-border-default">
			{rows.map((row) => {
				const choice = choices[row.source.key] ?? {
					copy: false,
					overwrite: false,
				};
				const available = Boolean(row.destination);
				const copyID = `volume-copy-${row.source.key}`;
				const overwriteID = `volume-overwrite-${row.source.key}`;
				return (
					<div key={row.source.key} className="p-4 flex flex-col gap-3">
						<label
							className="flex items-start gap-3 cursor-pointer"
							htmlFor={copyID}
						>
							<Checkbox
								id={copyID}
								checked={choice.copy}
								disabled={!available || disabled}
								onCheckedChange={(checked) =>
									onChange(row.source.key, {
										...choice,
										copy: checked === true,
									})
								}
							/>
							<div className="min-w-0">
								<div className="font-medium">{row.source.display_name}</div>
								{(row.source.mount_path || row.source.capacity) && (
									<div className="text-sm text-content-secondary">
										{[row.source.mount_path, row.source.capacity]
											.filter(Boolean)
											.join(" · ")}
									</div>
								)}
								{!available && (
									<div className="text-sm text-content-destructive mt-1">
										Destination does not expose a matching volume with logical
										key “{row.source.key}”.
									</div>
								)}
							</div>
						</label>
						<label
							className="flex items-center gap-3 pl-8 text-sm cursor-pointer"
							htmlFor={overwriteID}
						>
							<Checkbox
								id={overwriteID}
								checked={choice.overwrite}
								disabled={!choice.copy || !available || disabled}
								onCheckedChange={(checked) =>
									onChange(row.source.key, {
										...choice,
										overwrite: checked === true,
									})
								}
							/>
							<span>Overwrite existing files</span>
						</label>
						<div className="text-xs text-content-secondary pl-8">
							Destination-only files are never deleted.{" "}
							{row.source.excluded_paths?.length ?? 0} workspace-managed paths
							are protected from copying.
						</div>
					</div>
				);
			})}
		</div>
	);
};

export const OperationStatus: FC<{
	operation: WorkspaceVolumeCopyOperation;
}> = ({ operation }) => {
	if (operation.status === "succeeded") {
		return (
			<Alert severity="success" prominent>
				<div className="flex items-start gap-2">
					<CheckCircle2Icon className="size-icon-sm mt-0.5" />
					<div>
						<div className="font-medium">Persistent volume copy completed.</div>
						<div className="text-sm mt-1">
							Destination-only files were preserved. You can start the
							destination workspace now.
						</div>
					</div>
				</div>
			</Alert>
		);
	}
	if (operation.status === "failed" || operation.status === "canceled") {
		return (
			<Alert severity="error" prominent>
				<div className="flex items-start gap-2">
					<AlertCircleIcon className="size-icon-sm mt-0.5" />
					<div>
						<div className="font-medium">
							Persistent volume copy {operation.status}.
						</div>
						{operation.error && (
							<div className="text-sm mt-1">{operation.error}</div>
						)}
					</div>
				</div>
			</Alert>
		);
	}
	return (
		<Alert severity="info">
			<div className="font-medium">
				{operation.status === "pending" ? "Pending" : "Copying"}
			</div>
			<div className="text-sm mt-1">
				The operation continues on the server even if you close this page.
				Source and destination lifecycle changes remain locked until it
				finishes.
			</div>
		</Alert>
	);
};
