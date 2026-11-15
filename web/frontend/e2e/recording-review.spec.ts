import { expect, test, type Page } from "@playwright/test";
import { createRecordingFixture, fixtureToken, recordingApi, recordingPath, sampleLines } from "./fixtures/recording-review";

const speakersPath = `${recordingApi}/speakers`;

function delayResponse() {
    let release!: () => void;
    const pending = new Promise<void>((resolve) => { release = resolve; });
    return { pending, release };
}

async function openRecording(page: Page) {
    const fixture = createRecordingFixture();
    await page.addInitScript((token) => {
        localStorage.setItem("auth-storage", JSON.stringify({ state: { token }, version: 0 }));
    }, fixtureToken);
    await page.route("**/health", (route) => route.fulfill(fixture.respond("GET", "/health")));
    await page.route("**/api/**", async (route) => {
        const request = route.request();
        const reply = fixture.respond(
            request.method(),
            new URL(request.url()).pathname,
            request.headers().range,
            request.method() === "POST" ? request.postDataJSON() : undefined,
        );
        await route.fulfill(reply);
    });
    await page.goto(recordingPath);
    await expect(page.getByRole("tabpanel", { name: "Transcript" })).toContainText(sampleLines[0]);
    return fixture;
}

async function openRename(page: Page) {
    await page.getByRole("button", { name: "Recording actions" }).click();
    await page.getByRole("menuitem", { name: "Rename Speakers" }).click();
    // The actions menu can still be mounted during its exit animation.
    await expect(page.getByRole("menu", { includeHidden: true })).toHaveCount(0);
    return page.getByRole("dialog", { name: "Rename Speakers" });
}

test("speaker edits keep focus through queue polls and save or cancel explicitly", async ({ page }) => {
    const { requests } = await openRecording(page);
    const dialog = await openRename(page);
    const input = dialog.getByLabel("SPEAKER_00");
    await expect(dialog.getByLabel("SPEAKER_01")).toHaveValue("Morgan");
    await input.fill("Alex Rivera");
    await input.evaluate((element: HTMLInputElement) => element.setSelectionRange(1, 4));
    const requestsBeforePoll = { queue: requests.queue, speakerReads: requests.speakerReads };
    await expect.poll(() => requests.queue, { timeout: 8000 }).toBeGreaterThanOrEqual(requestsBeforePoll.queue + 2);
    await expect(input).toHaveValue("Alex Rivera");
    await expect(input).toBeFocused();
    expect(await input.evaluate((element: HTMLInputElement) => [element.selectionStart, element.selectionEnd])).toEqual([1, 4]);
    expect(requests.speakerReads).toBeLessThanOrEqual(requestsBeforePoll.speakerReads + 1);
    await dialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog).toBeHidden();
    expect(requests.saves).toHaveLength(1);
    expect(requests.saves[0]).toEqual(expect.arrayContaining([
        { original_speaker: "SPEAKER_00", custom_name: "Alex Rivera" },
        { original_speaker: "SPEAKER_01", custom_name: "Morgan" },
        { original_speaker: "SPEAKER_07", custom_name: "Guest from an earlier run" },
    ]));
    await openRename(page);
    await expect(input).toHaveValue("Alex Rivera");
    await input.fill("Discard this edit");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await openRename(page);
    await expect(input).toHaveValue("Alex Rivera");
    expect(requests.saves).toHaveLength(1);
});

test("rename waits for current speaker names before editing a warmed recording", async ({ page }) => {
    const fixture = await openRecording(page);
    await expect(page.getByText("Morgan", { exact: true }).first()).toBeVisible();
    const updatedNames = [
        { original_speaker: "SPEAKER_01", custom_name: "Morgan Ellis" },
        { original_speaker: "SPEAKER_07", custom_name: "Guest from an earlier run" },
        { original_speaker: "SPEAKER_12", custom_name: "Avery" },
    ];
    fixture.respond("POST", speakersPath, undefined, { mappings: updatedNames });

    const read = delayResponse();
    await page.route(`**${speakersPath}`, async (route) => {
        if (route.request().method() === "GET") await read.pending;
        await route.fallback();
    });

    try {
        const readStarted = page.waitForRequest(request =>
            new URL(request.url()).pathname === speakersPath && request.method() === "GET");
        const dialog = await openRename(page);
        await readStarted;
        await expect(dialog.getByRole("button", { name: "Save", exact: true })).toBeHidden();
        read.release();

        await expect(dialog.getByLabel("SPEAKER_01")).toHaveValue("Morgan Ellis");
        await expect(dialog.getByLabel("SPEAKER_12")).toHaveValue("Avery");
        await dialog.getByLabel("SPEAKER_00").fill("Alex Rivera");
        await dialog.getByRole("button", { name: "Save", exact: true }).click();
        await expect(dialog).toBeHidden();
        expect(fixture.requests.saves.at(-1)).toEqual(expect.arrayContaining([
            { original_speaker: "SPEAKER_00", custom_name: "Alex Rivera" },
            ...updatedNames,
        ]));
    } finally {
        read.release();
    }
});

test("rename remains locked until a delayed save finishes", async ({ page }) => {
    const { requests } = await openRecording(page);
    const save = delayResponse();
    await page.route(`**${speakersPath}`, async (route) => {
        if (route.request().method() === "POST") await save.pending;
        await route.fallback();
    });

    try {
        const dialog = await openRename(page);
        await dialog.getByLabel("SPEAKER_00").fill("Alex Rivera");
        const saveStarted = page.waitForRequest(request =>
            new URL(request.url()).pathname === speakersPath && request.method() === "POST");
        await dialog.getByRole("button", { name: "Save", exact: true }).click();
        await saveStarted;
        await expect(dialog.getByRole("button", { name: "Saving…" })).toBeDisabled();
        await expect(dialog.getByRole("button", { name: "Cancel" })).toBeDisabled();

        await page.keyboard.press("Escape");
        await expect(dialog).toBeVisible();
        await expect(dialog.getByRole("button", { name: "Close", exact: true })).toBeHidden();
        await page.mouse.click(4, 4);
        await expect(dialog).toBeVisible();
        await expect(dialog.getByLabel("SPEAKER_00")).toBeDisabled();

        save.release();
        await expect(dialog).toBeHidden();
        const reopened = await openRename(page);
        await expect(reopened.getByLabel("SPEAKER_00")).toHaveValue("Alex Rivera");
        expect(requests.saves).toHaveLength(1);
    } finally {
        save.release();
    }
});

test("Compare replaces the transcript and both panes follow the same playing audio", async ({ page }) => {
    await openRecording(page);
    await page.getByRole("tab", { name: "Compare" }).click();
    const compare = page.getByRole("tabpanel", { name: "Compare" });
    const primary = compare.getByRole("region", { name: "Primary transcript" });
    const comparison = compare.getByRole("region", { name: "Comparison transcript" });
    const follow = compare.getByRole("button", { name: "Follow playback" });
    const audio = page.locator("audio");
    await expect(primary).toBeVisible();
    await expect(comparison).toBeVisible();
    await expect(page.getByRole("tabpanel", { name: "Transcript" })).toBeHidden();
    await expect(compare.getByText(sampleLines[0], { exact: true })).toHaveCount(2);
    await expect(follow).toHaveAttribute("aria-pressed", "true");
    await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.readyState)).toBeGreaterThan(0);

    await page.getByRole("button", { name: "Play audio", exact: true }).click();
    await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.currentTime)).toBeGreaterThan(0);
    const outerScroll = page.locator("main .overflow-y-auto").first();
    const beforeScroll = await outerScroll.evaluate((element) => element.scrollTop);
    await audio.evaluate((element: HTMLAudioElement) => { element.currentTime = 48.25; });
    await expect(primary.locator('[aria-current="true"]')).toContainText(sampleLines[12]);
    await expect(comparison.locator('[aria-current="true"]')).toContainText(sampleLines[12]);
    await expect(primary.locator('[aria-current="true"] button')).toHaveAccessibleName("Play from 00:48");
    await expect(comparison.locator('[aria-current="true"] button')).toHaveAccessibleName("Play from 00:47");
    await expect.poll(() => primary.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
    await expect.poll(() => comparison.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
    expect(await outerScroll.evaluate((element) => element.scrollTop)).toBe(beforeScroll);

    await page.getByRole("button", { name: "Pause audio", exact: true }).click();
    await comparison.getByRole("button", { name: "Play from 00:39", exact: true }).click();
    await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => [element.paused, element.currentTime >= 39.35])).toEqual([false, true]);
    await primary.getByRole("button", { name: "Play from 00:12", exact: true }).click();
    await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => [element.paused, element.currentTime < 16])).toEqual([false, true]);
    await page.getByRole("button", { name: "Pause audio", exact: true }).click();

    await follow.click();
    await expect(follow).toHaveAttribute("aria-pressed", "false");
    await primary.evaluate((element) => { element.scrollTop = 0; });
    await comparison.evaluate((element) => { element.scrollTop = 0; });
    await audio.evaluate((element: HTMLAudioElement) => { element.currentTime = 52.25; });
    await expect(primary.locator('[aria-current="true"] button')).toHaveAccessibleName("Play from 00:52");
    await expect(comparison.locator('[aria-current="true"] button')).toHaveAccessibleName("Play from 00:51");
    expect(await primary.evaluate((element) => element.scrollTop)).toBe(0);
    expect(await comparison.evaluate((element) => element.scrollTop)).toBe(0);
    await audio.evaluate((element: HTMLAudioElement) => { element.currentTime = 3.75; });
    await expect(comparison.locator('[aria-current="true"] button')).toHaveAccessibleName("Play from 00:03");
    await expect(primary.locator('[aria-current="true"]')).toHaveCount(0);

    const dialog = await openRename(page);
    await expect(dialog.getByLabel("SPEAKER_01")).toHaveValue("Morgan");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toBeHidden();
    await page.getByRole("button", { name: "Recording actions" }).click();
    await page.getByRole("menuitem", { name: "Notes", exact: true }).click();
    const notes = page.getByRole("complementary", { name: "Recording notes" });
    await expect(notes).toBeVisible();
    await notes.getByRole("button", { name: "Close notes" }).click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.getByRole("tab", { name: "Transcript" }).click();
    await expect(page.getByRole("tabpanel", { name: "Transcript" })).toContainText(sampleLines[0]);
    await expect(compare).toBeHidden();
});
