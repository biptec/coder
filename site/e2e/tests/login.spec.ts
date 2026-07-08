import { test } from "@playwright/test";
import { expectUrl } from "../expectUrl";
import { login } from "../helpers";
import { beforeCoderTest } from "../hooks";

test.beforeEach(async ({ page }) => {
	beforeCoderTest(page);
});

test("login redirects to /workspaces", async ({ page }) => {
	await login(page);
	await expectUrl(page).toHavePathName("/workspaces");
});
