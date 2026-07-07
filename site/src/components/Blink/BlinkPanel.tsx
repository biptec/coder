import { ArrowUpIcon, PlusIcon, SparklesIcon, XIcon } from "lucide-react";
import {
	type FC,
	type KeyboardEvent,
	useEffect,
	useRef,
	useState,
} from "react";
import { cn } from "#/utils/cn";

export interface BlinkMessage {
	id: string;
	role: "user" | "assistant";
	content: string;
	timestamp: Date;
}

interface BlinkPanelProps {
	open: boolean;
	onClose: () => void;
	onNewChat: () => void;
	messages: BlinkMessage[];
	onSendMessage: (text: string) => void;
	isThinking: boolean;
}

export const BlinkPanel: FC<BlinkPanelProps> = ({
	open,
	onClose,
	onNewChat,
	messages,
	onSendMessage,
	isThinking,
}) => {
	const [inputValue, setInputValue] = useState("");
	const messagesEndRef = useRef<HTMLDivElement>(null);
	const inputRef = useRef<HTMLInputElement>(null);

	// Auto-scroll to bottom on new messages or thinking state change.
	const messageCount = messages.length;
	// biome-ignore lint/correctness/useExhaustiveDependencies: messageCount and isThinking are intentional scroll triggers
	useEffect(() => {
		messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
	}, [messageCount, isThinking]);

	// Focus input when panel opens.
	useEffect(() => {
		if (open) {
			inputRef.current?.focus();
		}
	}, [open]);

	const handleSend = () => {
		const text = inputValue.trim();
		if (!text) return;
		onSendMessage(text);
		setInputValue("");
	};

	const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
		if (e.key === "Enter" && !e.shiftKey) {
			e.preventDefault();
			handleSend();
		}
	};

	if (!open) return null;

	return (
		<div
			className={cn(
				"fixed bottom-20 right-6 z-50",
				"w-[400px] h-[600px] max-h-[80vh]",
				"flex flex-col",
				"rounded-xl shadow-2xl",
				"border border-border border-solid",
				"bg-surface-primary",
				"animate-in slide-in-from-bottom-2 fade-in duration-200",
			)}
		>
			{/* Header */}
			<div
				className={cn(
					"flex items-center justify-between",
					"px-4 py-3",
					"border-b border-border border-solid",
				)}
			>
				<div className="flex items-center gap-2">
					<SparklesIcon className="size-4 text-content-link" />
					<h2 className="text-sm font-semibold text-content-primary">Blink</h2>
				</div>
				<div className="flex items-center gap-1">
					<button
						type="button"
						onClick={onNewChat}
						aria-label="New chat"
						className={cn(
							"p-1.5 rounded-md",
							"text-content-secondary hover:text-content-primary",
							"hover:bg-surface-secondary",
							"transition-colors",
						)}
					>
						<PlusIcon className="size-4" />
					</button>
					<button
						type="button"
						onClick={onClose}
						aria-label="Close Blink"
						className={cn(
							"p-1.5 rounded-md",
							"text-content-secondary hover:text-content-primary",
							"hover:bg-surface-secondary",
							"transition-colors",
						)}
					>
						<XIcon className="size-4" />
					</button>
				</div>
			</div>

			{/* Messages */}
			<div className="flex-1 overflow-y-auto px-4 py-3 space-y-3">
				{messages.length === 0 && !isThinking && (
					<div className="flex flex-col items-center justify-center h-full text-center gap-3">
						<SparklesIcon className="size-8 text-content-disabled" />
						<div>
							<p className="text-sm font-medium text-content-primary">
								Hi, I'm Blink
							</p>
							<p className="text-xs text-content-secondary mt-1">
								Your AI assistant for Coder. Ask me anything about your
								workspaces, templates, or development environment.
							</p>
						</div>
					</div>
				)}

				{messages.map((msg) => (
					<div
						key={msg.id}
						className={cn(
							"flex",
							msg.role === "user" ? "justify-end" : "justify-start",
						)}
					>
						<div
							className={cn(
								"max-w-[85%] rounded-lg px-3 py-2",
								msg.role === "user"
									? "bg-surface-invert-primary text-surface-primary"
									: "bg-surface-secondary text-content-primary",
							)}
						>
							{msg.role === "assistant" && (
								<p className="text-xs font-medium text-content-secondary mb-1">
									Blink
								</p>
							)}
							<p className="text-sm whitespace-pre-wrap">{msg.content}</p>
						</div>
					</div>
				))}

				{isThinking && (
					<div className="flex justify-start">
						<div className="bg-surface-secondary rounded-lg px-3 py-2">
							<p className="text-xs font-medium text-content-secondary mb-1">
								Blink
							</p>
							<span className="inline-flex items-center gap-1 text-sm text-content-secondary">
								<span className="animate-bounce [animation-delay:0ms]">.</span>
								<span className="animate-bounce [animation-delay:150ms]">
									.
								</span>
								<span className="animate-bounce [animation-delay:300ms]">
									.
								</span>
							</span>
						</div>
					</div>
				)}

				<div ref={messagesEndRef} />
			</div>

			{/* Footer / Input */}
			<div className={cn("px-4 py-3", "border-t border-border border-solid")}>
				<div className="flex items-center gap-2">
					<input
						ref={inputRef}
						type="text"
						value={inputValue}
						onChange={(e) => setInputValue(e.target.value)}
						onKeyDown={handleKeyDown}
						placeholder="Ask Blink..."
						aria-label="Message Blink"
						className={cn(
							"flex-1 min-w-0",
							"px-3 py-2 text-sm",
							"rounded-lg border border-border border-solid",
							"bg-surface-primary text-content-primary",
							"placeholder:text-content-disabled",
							"focus:outline-none focus-visible:ring-2 focus-visible:ring-content-link",
						)}
					/>
					<button
						type="button"
						onClick={handleSend}
						disabled={!inputValue.trim()}
						aria-label="Send message"
						className={cn(
							"flex items-center justify-center",
							"w-8 h-8 rounded-lg",
							"bg-surface-invert-primary text-surface-primary",
							"hover:opacity-90 transition-opacity",
							"disabled:opacity-40 disabled:cursor-not-allowed",
						)}
					>
						<ArrowUpIcon className="size-4" />
					</button>
				</div>
			</div>
		</div>
	);
};
