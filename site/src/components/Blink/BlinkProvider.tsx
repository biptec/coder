import {
	type FC,
	type PropsWithChildren,
	createContext,
	useCallback,
	useContext,
	useEffect,
	useRef,
	useState,
} from "react";
import type { BlinkMessage } from "./BlinkPanel";

interface BlinkContextValue {
	enabled: boolean;
	open: boolean;
	toggle: () => void;
	close: () => void;
	messages: BlinkMessage[];
	sendMessage: (text: string) => void;
	startNewChat: () => void;
	isThinking: boolean;
	chatId: string | null;
}

const BlinkContext = createContext<BlinkContextValue | null>(null);

function readLocalStorage(key: string, fallback: string): string {
	try {
		return localStorage.getItem(key) ?? fallback;
	} catch {
		return fallback;
	}
}

function writeLocalStorage(key: string, value: string): void {
	try {
		localStorage.setItem(key, value);
	} catch {
		// Storage may be unavailable in some contexts.
	}
}

export const BlinkProvider: FC<
	PropsWithChildren<{ forceEnabled?: boolean }>
> = ({ children, forceEnabled }) => {
	const [enabled] = useState(
		() => forceEnabled || readLocalStorage("blink_enabled", "false") === "true",
	);
	const [open, setOpen] = useState(false);
	const [messages, setMessages] = useState<BlinkMessage[]>([]);
	const [isThinking, setIsThinking] = useState(false);
	const [chatId, setChatId] = useState<string | null>(
		() => readLocalStorage("blink_chat_id", "") || null,
	);

	// Track pending timeout so we can cancel it on unmount or new chat.
	const pendingTimeout = useRef<ReturnType<typeof setTimeout> | null>(null);

	useEffect(() => {
		return () => {
			if (pendingTimeout.current !== null) {
				clearTimeout(pendingTimeout.current);
			}
		};
	}, []);

	const toggle = useCallback(() => {
		setOpen((prev) => !prev);
	}, []);

	const close = useCallback(() => {
		setOpen(false);
	}, []);

	const sendMessage = useCallback((text: string) => {
		const userMessage: BlinkMessage = {
			id: crypto.randomUUID(),
			role: "user",
			content: text,
			timestamp: new Date(),
		};

		setMessages((prev) => [...prev, userMessage]);
		setIsThinking(true);

		// Cancel any previously pending response.
		if (pendingTimeout.current !== null) {
			clearTimeout(pendingTimeout.current);
		}

		// Stub: simulate assistant response after a short delay.
		// This will be wired to createChatMessage later.
		pendingTimeout.current = setTimeout(() => {
			pendingTimeout.current = null;
			const assistantMessage: BlinkMessage = {
				id: crypto.randomUUID(),
				role: "assistant",
				content:
					"This is a prototype response. Blink will be connected to the AI backend soon.",
				timestamp: new Date(),
			};
			setMessages((prev) => [...prev, assistantMessage]);
			setIsThinking(false);
		}, 1500);
	}, []);

	const startNewChat = useCallback(() => {
		// Cancel any pending stub response.
		if (pendingTimeout.current !== null) {
			clearTimeout(pendingTimeout.current);
			pendingTimeout.current = null;
		}
		setMessages([]);
		setIsThinking(false);
		const newId = crypto.randomUUID();
		setChatId(newId);
		writeLocalStorage("blink_chat_id", newId);
	}, []);

	return (
		<BlinkContext.Provider
			value={{
				enabled,
				open,
				toggle,
				close,
				messages,
				sendMessage,
				startNewChat,
				isThinking,
				chatId,
			}}
		>
			{children}
		</BlinkContext.Provider>
	);
};

export function useBlinkContext(): BlinkContextValue {
	const ctx = useContext(BlinkContext);
	if (!ctx) {
		throw new Error("useBlinkContext must be used within a BlinkProvider");
	}
	return ctx;
}
