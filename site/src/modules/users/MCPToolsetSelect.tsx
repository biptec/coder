import type { FC } from "react";
import type { MCPToolset } from "#/api/typesGenerated";
import { Label } from "#/components/Label/Label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "#/components/Select/Select";

const toolsetOptions: ReadonlyArray<{
	value: MCPToolset;
	label: string;
	description: string;
}> = [
	{
		value: "developer",
		label: "Developer",
		description: "Curated coding tools with concise assistant-facing names.",
	},
	{
		value: "admin",
		label: "Admin",
		description:
			"Full Coder Remote MCP toolset, including administrative and lifecycle tools.",
	},
	{
		value: "readonly",
		label: "Read only",
		description:
			"Read-only inspection tools; no file writes or command execution.",
	},
];

type MCPToolsetSelectProps = {
	value: MCPToolset;
	onChange: (value: MCPToolset) => void;
	disabled?: boolean;
	id?: string;
};

export const MCPToolsetSelect: FC<MCPToolsetSelectProps> = ({
	value,
	onChange,
	disabled = false,
	id = "mcp_toolset",
}) => {
	const selected = toolsetOptions.find((option) => option.value === value);

	return (
		<div className="flex max-w-sm flex-col gap-2">
			<Label htmlFor={id}>Remote MCP toolset</Label>
			<Select
				value={value}
				onValueChange={(nextValue) => onChange(nextValue as MCPToolset)}
				disabled={disabled}
			>
				<SelectTrigger id={id} data-testid="mcp-toolset-input">
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					{toolsetOptions.map((option) => (
						<SelectItem key={option.value} value={option.value}>
							{option.label}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			<p className="text-xs text-cont-secondary">{selected?.description}</p>
			<p className="text-xs text-content-secondary">
				Controls which tools external assistants see when they connect to Coder
				Remote MCP using this user&apos;s API token. Coder RBAC permissions
				remain independent.
			</p>
		</div>
	);
};
