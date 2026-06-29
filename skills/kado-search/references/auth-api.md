# Auth API

Kado is hosted at `https://kado.so`

Prefer `KADO_API_KEY` when it is already present in the environment or supplied by the user. API keys and device-login tokens are both sent as bearer tokens:

```bash
export KADO_BASE_URL="https://kado.so"
export KADO_AUTH_HEADER="Authorization: Bearer $KADO_API_KEY"
```

```powershell
$env:KADO_BASE_URL = 'https://kado.so'
$env:KADO_AUTH_HEADER = "Authorization: Bearer $env:KADO_API_KEY"
```

If no API key is available, use device login. The examples below are scripts an agent can adapt or run from a temporary location. Do not commit them to the project.

## Example: Auth Status In Node

This checks for `KADO_API_KEY`, then for a cached token in user-local storage. It prints JSON and can emit shell setup commands.

```js
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const BASE_URL = "https://kado.so";
const mode = process.argv[2] || "json";

function tokenFilePath() {
  if (process.platform === "win32") {
    const root = process.env.LOCALAPPDATA || process.env.APPDATA || path.join(os.homedir(), "AppData", "Local");
    return path.join(root, "Kado", "agent-auth.json");
  }
  const root = process.env.XDG_STATE_HOME || path.join(os.homedir(), ".local", "state");
  return path.join(root, "kado", "agent-auth.json");
}

function readCachedToken() {
  try {
    const data = JSON.parse(fs.readFileSync(tokenFilePath(), "utf8"));
    if (!data.access_token) return null;
    if (data.expires_at && Number(data.expires_at) <= Date.now() / 1000 + 60) return null;
    return data.access_token;
  } catch {
    return null;
  }
}

const token = process.env.KADO_API_KEY || readCachedToken();

if (!token) {
  console.log(JSON.stringify({
    logged_in: false,
    base_url: BASE_URL,
    message: "Kado user is not logged in. Use device login or set KADO_API_KEY."
  }));
  process.exit(0);
}

const authHeader = `Authorization: Bearer ${token}`;

if (mode === "--emit-powershell") {
  console.log(`$env:KADO_BASE_URL = 'https://kado.so'`);
  console.log(`$env:KADO_AUTH_HEADER = '${authHeader.replaceAll("'", "''")}'`);
} else if (mode === "--emit-sh") {
  console.log(`export KADO_BASE_URL='https://kado.so'`);
  console.log(`export KADO_AUTH_HEADER='${authHeader.replaceAll("'", "'\\''")}'`);
} else {
  console.log(JSON.stringify({ logged_in: true, base_url: BASE_URL }));
}
```

## Example: Device Login In Node

This starts device login, shows the verification URL to the user, polls until approval, and stores the returned token outside the current project.

```js
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const BASE_URL = "https://kado.so";
const CLIENT_ID = "kado-cli";

function tokenFilePath() {
  if (process.platform === "win32") {
    const root = process.env.LOCALAPPDATA || process.env.APPDATA || path.join(os.homedir(), "AppData", "Local");
    return path.join(root, "Kado", "agent-auth.json");
  }
  const root = process.env.XDG_STATE_HOME || path.join(os.homedir(), ".local", "state");
  return path.join(root, "kado", "agent-auth.json");
}

async function postJson(url, body) {
  const response = await fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json", accept: "application/json" },
    body: JSON.stringify(body)
  });
  const data = await response.json();
  if (!response.ok && !data.error) throw new Error(`Kado request failed: HTTP ${response.status}`);
  return data;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function storeToken(data) {
  const tokenFile = tokenFilePath();
  const tokenDir = path.dirname(tokenFile);
  const output = { ...data };
  if (output.expires_in) output.expires_at = Date.now() / 1000 + Number(output.expires_in);
  fs.mkdirSync(tokenDir, { recursive: true, mode: 0o700 });
  fs.writeFileSync(tokenFile, `${JSON.stringify(output)}\n`, { mode: 0o600 });
  return tokenFile;
}

const code = await postJson(`${BASE_URL}/api/auth/device/code`, { client_id: CLIENT_ID });
const verificationUrl = code.verification_uri_complete || code.verification_uri;
let intervalSeconds = Number(code.interval || 5);

if (!code.device_code || !verificationUrl) throw new Error("Kado did not return a device code and verification URL.");

console.error(`Open this URL to log in to Kado:\n${verificationUrl}\n`);

while (true) {
  await sleep(intervalSeconds * 1000);
  const token = await postJson(`${BASE_URL}/api/auth/device/token`, {
    client_id: CLIENT_ID,
    device_code: code.device_code,
    grant_type: "urn:ietf:params:oauth:grant-type:device_code"
  });

  if (token.access_token) {
    console.log(JSON.stringify({ logged_in: true, base_url: BASE_URL, source: storeToken(token) }));
    break;
  }

  if (token.error === "authorization_pending") continue;
  if (token.error === "slow_down") {
    intervalSeconds += 5;
    continue;
  }
  throw new Error(`Kado device login failed: ${token.error || "unknown_error"}`);
}
```

Auth precedence:

1. `KADO_API_KEY` from the environment.
2. Cached device login token.

Never print, log, commit, or persist credentials in the current repository. Never use browser cookies or browser session state.
