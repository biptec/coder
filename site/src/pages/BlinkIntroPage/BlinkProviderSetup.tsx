import { type CSSProperties, type FC, useState } from "react";
import { useMutation, useQueryClient } from "react-query";
import { createAIProviderMutation } from "#/api/queries/aiProviders";
import { createChatModelConfig } from "#/api/queries/chats";
import type { AIProviderType } from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { Input } from "#/components/Input/Input";
import { Label } from "#/components/Label/Label";
import { Spinner } from "#/components/Spinner/Spinner";
import { cn } from "#/utils/cn";

interface BlinkProviderSetupProps {
	onComplete: () => void;
	onSkip: () => void;
}

interface ProviderOption {
	type: AIProviderType;
	name: string;
	defaultBaseUrl: string;
	models: string[];
}

const providers: ProviderOption[] = [
	{
		type: "anthropic",
		name: "Anthropic",
		defaultBaseUrl: "https://api.anthropic.com",
		models: [
			"claude-sonnet-4-20250514",
			"claude-3-5-haiku-20241022",
			"claude-3-5-sonnet-20241022",
		],
	},
	{
		type: "openai",
		name: "OpenAI",
		defaultBaseUrl: "https://api.openai.com/v1",
		models: ["gpt-4o", "gpt-4o-mini", "o3-mini"],
	},
	{
		type: "google",
		name: "Google",
		defaultBaseUrl: "https://generativelanguage.googleapis.com/v1beta",
		models: ["gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash"],
	},
];

/**
 * Provider setup shown as the first step of the Blink intro flow.
 * Creates an AI provider and a default model config so Blink has a
 * working backend before the user first talks to it.
 */
export const BlinkProviderSetup: FC<BlinkProviderSetupProps> = ({
	onComplete,
	onSkip,
}) => {
	const queryClient = useQueryClient();
	const createProvider = useMutation(createAIProviderMutation(queryClient));
	const createModelConfig = useMutation(createChatModelConfig(queryClient));

	const [selectedProvider, setSelectedProvider] =
		useState<ProviderOption | null>(null);
	const [apiKey, setApiKey] = useState("");
	const [baseUrl, setBaseUrl] = useState("");
	const [selectedModel, setSelectedModel] = useState("");

	const isPending = createProvider.isPending || createModelConfig.isPending;
	const error = createProvider.error || createModelConfig.error;

	const handleProviderSelect = (provider: ProviderOption) => {
		setSelectedProvider(provider);
		setBaseUrl(provider.defaultBaseUrl);
		setSelectedModel(provider.models[0]);
	};

	const handleSave = () => {
		if (!selectedProvider || !apiKey || !selectedModel) {
			return;
		}
		createProvider.mutate(
			{
				type: selectedProvider.type,
				name: selectedProvider.type,
				display_name: selectedProvider.name,
				enabled: true,
				base_url: baseUrl,
				api_keys: [apiKey],
			},
			{
				onSuccess: (provider) => {
					createModelConfig.mutate(
						{
							ai_provider_id: provider.id,
							model: selectedModel,
							enabled: true,
							is_default: true,
						},
						{
							onSuccess: onComplete,
						},
					);
				},
			},
		);
	};

	return (
		<div className="flex flex-col gap-6 w-full">
			<header className="text-center">
				<h2 className="text-2xl font-semibold m-0">Set up Blink</h2>
				<p className="text-sm text-content-secondary mt-2 mb-0">
					Blink needs an AI provider to work. Connect one now so it's ready when
					you are.
				</p>
			</header>

			{/* Provider cards */}
			<div className="grid grid-cols-3 gap-3">
				{providers.map((provider) => (
					<button
						key={provider.type}
						type="button"
						onClick={() => handleProviderSelect(provider)}
						className={cn(
							"flex flex-col items-center gap-2 p-4 rounded-lg border border-solid cursor-pointer transition-colors text-center bg-transparent",
							selectedProvider?.type === provider.type
								? "border-content-link bg-surface-secondary"
								: "border-border hover:border-border-secondary",
						)}
					>
						<span className="text-sm font-medium text-content-primary">
							{provider.name}
						</span>
					</button>
				))}
			</div>

			{selectedProvider && (
				<div className="flex flex-col gap-4">
					{/* API Key */}
					<div className="flex flex-col gap-2">
						<Label htmlFor="blink-api-key">API Key</Label>
						<Input
							id="blink-api-key"
							type="text"
							value={apiKey}
							onChange={(e) => setApiKey(e.target.value)}
							placeholder={`Enter your ${selectedProvider.name} API key`}
							style={{ WebkitTextSecurity: "disc" } as CSSProperties}
						/>
					</div>

					{/* Base URL */}
					<div className="flex flex-col gap-2">
						<Label htmlFor="blink-base-url">Base URL (optional)</Label>
						<Input
							id="blink-base-url"
							type="text"
							value={baseUrl}
							onChange={(e) => setBaseUrl(e.target.value)}
							placeholder="https://..."
						/>
					</div>

					{/* Model selector */}
					<div className="flex flex-col gap-2">
						<Label>Model</Label>
						<div className="flex flex-col gap-1">
							{selectedProvider.models.map((model) => (
								<label
									key={model}
									htmlFor={`blink-model-${model}`}
									className={cn(
										"flex items-center gap-3 px-3 py-2 rounded-md cursor-pointer transition-colors",
										selectedModel === model
											? "bg-surface-secondary"
											: "hover:bg-surface-secondary",
									)}
								>
									<input
										type="radio"
										id={`blink-model-${model}`}
										name="blink-model"
										value={model}
										checked={selectedModel === model}
										onChange={() => setSelectedModel(model)}
										className="accent-content-link"
									/>
									<span className="text-sm">{model}</span>
								</label>
							))}
						</div>
					</div>

					{Boolean(error) && (
						<p className="text-sm text-content-destructive m-0">
							Failed to save the provider or model. Check your credentials and
							try again.
						</p>
					)}
				</div>
			)}

			<div className="flex justify-between items-center pt-2">
				<Button variant="subtle" onClick={onSkip}>
					Skip for now
				</Button>
				{selectedProvider ? (
					<Button disabled={!apiKey || isPending} onClick={handleSave}>
						<Spinner loading={isPending} />
						Save &amp; Continue
					</Button>
				) : (
					<span className="text-xs text-content-secondary">
						Select a provider above to continue
					</span>
				)}
			</div>
		</div>
	);
};
