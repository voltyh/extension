# Gemini CLI Testing Guide

This file is the running manual for every CLI test build we produce.

If you want to know:
- which test build you are using,
- what software you need,
- how to run it on Windows or Linux,
- what “success” looks like,

start here.

---

## 1. Current test build

- **Current CLI test version:** `v0.2.0`
- **Command to verify your build version:**

```bash
go run ./cmd/gemini-exporter -version
```

Expected output:

```text
v0.2.0
```

---

## 2. What this test build does

This test build can:

- run as a normal CLI tool,
- connect to a Chrome or Edge window that is already logged in to Gemini,
- reuse that browser session instead of asking you to manually copy cookies,
- export one Gemini chat into a ZIP file,
- write:
  - `conversation.json`
  - `index.html`
  - `manifest.json`
  - `logs/export.log`
  - `media/*` when media is found

This test build does **not** require C++ compilation.
It is written in **Go**, and I can build and test that here.

---

## 3. Software you need

You need these things:

### Required

1. **Go 1.24 or newer**
2. **Google Chrome** or **Microsoft Edge**
3. A **Gemini account already signed in inside that browser**

### Nice to have

4. A terminal:
   - **Windows:** PowerShell
   - **Linux:** Bash

---

## 4. How to install the needed software

### Windows

#### Install Go
1. Open your browser.
2. Go to: `https://go.dev/dl/`
3. Download the Windows installer for Go 1.24 or newer.
4. Run the installer.
5. Accept the default options.
6. Open **PowerShell**.
7. Check that Go works:

```powershell
go version
```

You should see a Go version printed.

#### Install Chrome
If Chrome is not already installed:
1. Go to: `https://www.google.com/chrome/`
2. Download and install it.

You can also use Edge if you prefer.

---

### Linux

#### Install Go
Go to:

`https://go.dev/dl/`

Download Go 1.24 or newer for Linux and install it using the instructions on that page.

Then check:

```bash
go version
```

#### Install Chrome
Install Google Chrome using your distro’s normal method, or use Microsoft Edge if you already have it.

---

## 5. Before you run the CLI

You must use a browser window started with **remote debugging enabled**.

This is important.

The CLI does **not** steal cookies from disk.
Instead, it talks to the already-running browser and uses the logged-in session that is already open there.

That is the current test method.

---

## 6. Windows test steps

### Step A — Start Chrome with remote debugging

Open **PowerShell** and run:

```powershell
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222 --user-data-dir="$env:TEMP\gemini-export-profile"
```

What this does:
- opens a separate Chrome profile,
- enables remote debugging on port `9222`,
- keeps this test session separate from your normal Chrome profile.

### Step B — Log in to Gemini

In that Chrome window:
1. Open `https://gemini.google.com/app`
2. Sign in
3. Open the chat you want to export
4. Wait until the page looks fully loaded

### Step C — Open the repository folder

In PowerShell:

```powershell
cd /home/runner/work/extension/extension/cli
```

If you are testing on your own machine and the repo is somewhere else, go to **your** `cli` folder instead.

### Step D — Confirm the CLI version

```powershell
go run ./cmd/gemini-exporter -version
```

Expected:

```text
v0.2.0
```

### Step E — Run a real Gemini export

```powershell
go run ./cmd/gemini-exporter -collector gemini -remote-debugging-url http://127.0.0.1:9222 -output .\gemini-export-v0.2.0.zip
```

If you know the Gemini chat id, use:

```powershell
go run ./cmd/gemini-exporter -collector gemini -conversation-id YOUR_CHAT_ID -remote-debugging-url http://127.0.0.1:9222 -output .\gemini-export-v0.2.0.zip
```

### Step F — Check if it worked

You should see summary lines like:

```text
export complete: ...
tool version: v0.2.0
collector: gemini
messages: ...
media: ...
failures: 0
```

Then open the ZIP file and confirm it contains:

- `conversation.json`
- `index.html`
- `manifest.json`
- `logs/export.log`

---

## 7. Linux test steps

### Step A — Start Chrome with remote debugging

```bash
google-chrome --remote-debugging-port=9222 --user-data-dir=/tmp/gemini-export-profile
```

### Step B — Log in to Gemini

In that browser:
1. Open `https://gemini.google.com/app`
2. Sign in
3. Open the chat you want to export
4. Wait until the page is fully loaded

### Step C — Open the CLI folder

```bash
cd /home/runner/work/extension/extension/cli
```

If your repo is in another place on your own machine, use your real path.

### Step D — Confirm the CLI version

```bash
go run ./cmd/gemini-exporter -version
```

Expected:

```text
v0.2.0
```

### Step E — Run a real Gemini export

```bash
go run ./cmd/gemini-exporter -collector gemini -remote-debugging-url http://127.0.0.1:9222 -output ./gemini-export-v0.2.0.zip
```

If you know the chat id:

```bash
go run ./cmd/gemini-exporter -collector gemini -conversation-id YOUR_CHAT_ID -remote-debugging-url http://127.0.0.1:9222 -output ./gemini-export-v0.2.0.zip
```

### Step F — Check if it worked

Look for:

```text
export complete: ...
tool version: v0.2.0
collector: gemini
messages: ...
media: ...
failures: 0
```

Then inspect the ZIP file.

---

## 8. How to build versioned binaries

You asked for versioned builds so it is easy to see where you were and go back later.

Use this pattern:

### Linux binary

```bash
cd /home/runner/work/extension/extension/cli
go build -ldflags "-X github.com/voltyh/extension/cli/internal/version.Build=v0.2.0-test1" -o ./bin/gemini-exporter-v0.2.0-test1-linux ./cmd/gemini-exporter
```

### Windows binary

```bash
cd /home/runner/work/extension/extension/cli
GOOS=windows GOARCH=amd64 go build -ldflags "-X github.com/voltyh/extension/cli/internal/version.Build=v0.2.0-test1" -o ./bin/gemini-exporter-v0.2.0-test1-windows.exe ./cmd/gemini-exporter
```

After that, check the version:

```bash
./bin/gemini-exporter-v0.2.0-test1-linux -version
```

or on Windows:

```powershell
.\bin\gemini-exporter-v0.2.0-test1-windows.exe -version
```

Expected:

```text
v0.2.0-test1
```

---

## 9. Test version log

Add a new section here for each new test build.

### v0.2.0

Status:
- first version with a real Gemini CLI collector path
- uses active-session browser attach through remote debugging
- prints build version with `-version`
- defaults export ZIP names to a versioned filename

What to test:
- version output works
- mock export still works
- live Gemini export works
- ZIP contains expected files

---

## 10. If it fails

Check these things in order:

1. Did you start Chrome/Edge with `--remote-debugging-port=9222`?
2. Did you log in to Gemini in **that same browser window**?
3. Is the Gemini chat already open?
4. Did `go run ./cmd/gemini-exporter -version` print the version you expected?
5. Did you point the CLI at the right debugging URL?
6. Did the output ZIP get created?
7. Does `logs/export.log` exist inside the ZIP?

If the tool still fails, save:
- the terminal output
- the ZIP file if one was created
- the `logs/export.log`

Those three things are the first things to compare between test versions.
