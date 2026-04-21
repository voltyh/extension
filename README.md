# Gemini Master Exporter

A Chrome extension that exports your entire Gemini conversation history — including text, images, attachments, and AI-generated media — into a single, fully self-contained HTML archive.

---

## Features

- **Two-phase export**: Phase 1 smart-scrolls up to load the full chat history; Phase 2 crawls every message and writes the archive in real time.
- **Auto-start Phase 2**: Once Phase 1 reaches the top, Phase 2 begins automatically (optional checkbox).
- **Deep media capture**: Inline images are extracted as embedded base64 data — no external dependencies in the output file.
- **5-action attachment wizard**: For each file attachment the wizard offers Auto-Read Text, Extract Image, Extract Video, Import from Disk (drag-and-drop), or Mark Missing.
- **Drag-and-drop file import**: Drop any text or binary file directly into the wizard to embed it inline in the archive.
- **Draggable floating UI**: The launcher button, main panel, and wizard dialog are all freely repositionable.
- **Pause / Resume**: Suspend processing at any point without losing progress.
- **Terminate**: Cleanly stops the export, writes closing HTML so the partial file remains valid, and releases all system resources.
- **Anti-throttle engine**: Plays a silent audio loop and requests the Screen Wake Lock API to prevent Chrome from throttling or sleeping during long exports.
- **Media appendix**: A jump-to-message index is written at the end of every export, listing every captured or missing file.
- **Diagnostic log export**: A `.txt` log of every operation is saved automatically when the export completes.
- **Memory-safe streaming**: The HTML archive is streamed to disk block-by-block via the File System Access API — large conversations never balloon RAM.

---

## Installation (Developer / Unpacked)

1. Download or clone this repository.
2. Open Chrome and navigate to `chrome://extensions`.
3. Enable **Developer mode** (toggle in the top-right corner).
4. Click **Load unpacked** and select the folder containing `manifest.json`.
5. Navigate to [gemini.google.com](https://gemini.google.com) — the **Launch Exporter v45** button appears in the bottom-left corner of the page.

---

## Usage

### Phase 1 — Load full history
Click **Phase 1: Smart Load (Up)**. The extension scrolls to the top of the conversation, triggering lazy-loaded messages to appear. A progress counter shows chunks being loaded. When the top is reached (5 consecutive checks with no new content), Phase 2 starts automatically if the checkbox is enabled.

### Phase 2 — Export
Click **Phase 2: Save** (or let it auto-start). A native save dialog opens — choose a location and filename. The extension then crawls every message from top to bottom:

- Plain text and code blocks are captured directly from the DOM.
- Inline images are opened in their fullscreen viewer and extracted as base64.
- File attachment chips trigger the **Attachment Wizard**:
  - **Auto-Read Text** — opens the file viewer and reads text content.
  - **Extract Image** — opens the image viewer and captures it as base64.
  - **Extract Video** — opens the video, gives you time to download it, then lets you drag-and-drop the file into the wizard.
  - **Import File** — drag any file from your desktop to embed it directly.
  - **Mark Missing** — skips the file and records it in the Missing Files appendix.

### Pause and Resume
Use the **Pause** button at any time to freeze processing. The current message outline is cleared. Click **Resume** to continue from exactly where you left off.

### Terminate
Click **Terminate** in the panel header to stop immediately. The partial output file is closed with valid closing HTML tags so it can still be opened. The launcher button reappears so you can start a new session.

---

## Output File Structure

The exported `.html` file is a single standalone document. It requires no internet connection to view.

| Section | Description |
|---|---|
| Navigation banner | Jump-link to the Media Appendix at the bottom |
| Message blocks | Every user query and model response, styled and labelled |
| Inline media | Embedded base64 images and videos, shown inside the message |
| Deep-fetch containers | Extracted text files shown in dark-themed code blocks |
| Media Appendix | Checklist of all media found with jump links and Drive links for missing files |

---

## File Structure

```
Gemini_Exporter_Extension/
├── manifest.json      # Extension manifest (Manifest V3)
├── background.js      # Service worker — proxies cross-origin image fetches via chrome.runtime
├── content.js         # Main injected script — all UI and export logic
└── README.md          # This file
```

---

## Permissions

| Permission | Reason |
|---|---|
| `activeTab` | Read and interact with the active Gemini tab |
| `host_permissions: <all_urls>` | Allow the background service worker to fetch images from Google's CDN for base64 conversion |

---

## Known Limitations

- **Google Docs attachments** embedded in iframes are blocked by CORS and cannot be auto-read. The wizard will fall back and offer the Import option instead.
- **Video files** cannot be extracted programmatically. The wizard opens the player, gives you time to right-click-save the video, and then lets you drag-and-drop the saved file to embed it.
- The File System Access API (`showSaveFilePicker`) is required for streaming exports. This API is available in Chrome 86+ and Edge 86+. It is not available in Firefox.
- Very large base64-embedded videos will increase the output file size significantly.

---

## Development Notes

- All UI elements use `data-harvester-*` attributes to avoid collisions with Gemini's own DOM.
- The drag utility uses a 5 px movement threshold to distinguish a drag from a click, preventing accidental launches when repositioning the launcher button.
- Log entries are capped at 5 000 in the array and 80 DOM nodes to prevent memory growth during long sessions.

---

## License

MIT — free to use, modify, and distribute.
