import {
	maxActivityPageSize,
	minActivityPageSize,
} from "./activityPreferences";

export const parseActivityPage = (
	raw: string,
	current: number,
	totalPages: number,
): number => {
	const parsed = Number(raw);
	if (!Number.isInteger(parsed)) return current;
	return Math.min(Math.max(1, totalPages), Math.max(1, parsed));
};

export const parseActivityPageSize = (raw: string, current: number): number => {
	const parsed = Number(raw);
	if (!Number.isInteger(parsed)) return current;
	return Math.min(maxActivityPageSize, Math.max(minActivityPageSize, parsed));
};

export const getClampedActivityPage = (
	page: number,
	totalPages: number,
	isPlaceholderData: boolean,
): number | undefined => {
	if (isPlaceholderData || page <= totalPages) return undefined;
	return totalPages;
};
