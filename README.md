# Power Automate run history

Export cloud flow run history to the same CSV layout as the Power Automate portal download: one row per run, with status, error, trigger, and every action's status, input, output, and timestamps.

The portal file is produced by the Flow Service API (`https://api.flow.microsoft.com`), not by Dataverse. The Dataverse `flowrun` table stores run metadata only, so it cannot fill the per-action columns. Cloud flow run history from this API is kept for about 28 days.

Run inputs and outputs can contain document and patient data. The tool writes that data only to the CSV. It does not print payloads. Generated CSVs are gitignored.

## Export

```bash
go run ./cmd/flowruns export \
  --environment e514c2c1-1d32-e571-a29f-8ba50fc3801c \
  --flow e2738c2b-3425-453b-a705-75049ce8c588 \
  --status Failed \
  --out ./out
```

`--status all` writes one CSV per status: Failed, Succeeded, Cancelled, Running, TimedOut, Aborted, and Suspended.

```bash
go run ./cmd/flowruns export \
  --environment e514c2c1-1d32-e571-a29f-8ba50fc3801c \
  --flow e2738c2b-3425-453b-a705-75049ce8c588 \
  --status all \
  --since 2026-10-01T00:00:00Z \
  --until 2026-10-28T00:00:00Z \
  --timezone America/New_York \
  --limit 100 \
  --out ./out
```

`--since` is inclusive and `--until` is exclusive, both RFC3339, and both filter `startTime`. `--limit` caps runs per status. The portal download in the sample stopped at 100 runs; omit `--limit` to page through every run still retained by the API. Timestamps in the file use `--timezone`, or the machine local zone when it is omitted. `--as-admin` calls the environment admin route when you can administer the environment but do not own the flow.

The output name matches the portal:

`flow-{flowId}-status eq '{status}'-{utc timestamp}z.csv`

## Sign in

The Flow Service API allows delegated user permissions only. `Flows.Read.All` is enough to read runs. Pick one of these:

1. Set `FLOW_ACCESS_TOKEN` to a bearer token for `https://service.flow.microsoft.com/`.
2. Sign in with Azure CLI. The tool runs `az account get-access-token --resource https://service.flow.microsoft.com/`.

```bash
az login
go run ./cmd/flowruns export --environment <env> --flow <flow> --status Failed
```

3. Device code, when `FLOW_TENANT_ID` and `FLOW_CLIENT_ID` are set. Register a public client in Entra ID, enable public client flows, add the delegated Flow Service permission `Flows.Read.All`, and grant admin consent.

```bash
export FLOW_TENANT_ID=<tenant>
export FLOW_CLIENT_ID=<public-client-id>
go run ./cmd/flowruns export --environment <env> --flow <flow> --status all
```

The signed-in user must be able to open the flow, or be an environment admin when `--as-admin` is set.

## CSV layout

The file starts with a UTF-8 BOM and a `sep=,` line, then uses CRLF. Fixed columns are run start, run end, run ID, status, error, and the maker portal link. Each action adds status, method, input, output, start time, and end time. The trigger columns are named `Trigger status`, `Trigger method`, and so on. Other actions use `{ActionName} - status`. Method is left blank, matching the portal export. Objects and arrays are indented JSON. Numbers, booleans, and strings are written as plain text.
