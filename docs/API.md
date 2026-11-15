EVA Station exposes JSON APIs for the operator console, public phone assets and liveness, CSV exports, printable HTML reports, and two WebSocket upgrades. This document describes the registered HTTP contract and the validation actually performed by the handlers and their engine/store calls; it does not imply additional endpoints or validation.

Source of truth: `internal/server/server.go`, `internal/server/auth.go`, `internal/server/api.go`, `internal/server/reports.go`, `internal/server/pairing.go`, `internal/server/outbox.go`, `internal/server/hub.go`, `internal/engine/engine.go`, `internal/engine/admin.go`, `internal/engine/blackbox.go`, `internal/engine/reports.go`, `internal/model/model.go`, `internal/store/access.go`, `internal/store/store.go`, `internal/store/migrate.go`, `internal/store/schema.sql`, `internal/store/backup.go`, `internal/sim/sim.go`, `internal/ws/ws.go`.

## Authentication and roles

The station PC is the built-in `operator`: the caller's socket IP must be loopback or equal an address of a local network interface. This check precedes token authentication; forwarded headers are not consulted. Remote callers use the bootstrap operator token or an enabled personal user's token. Bootstrap token selection is explicit configuration, then `EVA_OPERATOR_TOKEN`, then `operator.token` in the data directory (generated if necessary). Personal tokens are stored as SHA-256 hashes and resolve to permissions from the `roles` table, cached once by the server.

| Role seeded by migration | view | medical | respond | command | admin |
| --- | --- | --- | --- | --- | --- |
| operator | Yes | Yes | Yes | Yes | Yes |
| surgeon | Yes | Yes | Yes | No | No |

The station PC and bootstrap token receive all five permissions directly. `view` includes clinical live state; it is not a public/redacted permission. No role-edit API is registered.

Open `/?operator=TOKEN` or `/?token=TOKEN` with either valid console token to receive `eva_operator`: `Path=/`, `HttpOnly`, `SameSite=Strict`, 12-hour `Max-Age`, and `Secure` only when the request uses TLS. The response is `303` to `/`. `operator` takes precedence over `token`. Query-token login is implemented by the console static handler, not by API handlers. Other requests accept `Authorization: Bearer TOKEN` (case-sensitive prefix), falling back to the cookie only when that prefix is absent. Logout clears the cookie; it does not revoke a token or the local-PC shortcut.

Permission wrappers return `401` for unauthenticated callers and `403` for missing permission. They also reject cross-origin mutating requests (anything except GET/HEAD) and WebSocket upgrades with `403`. `Sec-Fetch-Site` must be absent, `same-origin`, or `none`; if `Origin` is present, its parsed host, including port, must equal the request host, case-insensitively. The scheme is not compared. Missing both headers is allowed for authorized scripts. `/ws/device` and logout have no such wrapper. There is no wildcard CORS policy.

Wrapped mutations install a 1 MiB `MaxBytesReader`. `readJSON` independently reads at most 1 MiB and unmarshals it: empty bodies are accepted, unknown JSON fields are ignored, and handlers that check decoding errors return `400`. Because the inner reader stops at the cap, excess bytes are not reliably detected; a valid JSON prefix filling the cap can be accepted. There is no explicit `413` response contract. Mission actions, alert acknowledgement, and simulator clear ignore decode errors and may act using zero/partially decoded fields.

`audit_log` records all wrapper-denied requests, all wrapped mutations, and medical/admin reads, with actor, role, socket IP, method/path, and resulting HTTP status. Routine successful view reads are not audited. Console query-token login records `303` on success or `401` on an invalid supplied token; ordinary static-page access is not audited by `operatorPage`. Logout, public health/assets, and successful console WebSocket admission are not audited by the wrapper. Device authentication failures record `HELLO <code>` with result `0`; successful pairing records `PAIR` with result `1`. Malformed `bad_hello` and device-creation failures do not use those authentication audit calls. Audit write errors are ignored. This security log is distinct from `oplog` and the black-box chain.

## Table conventions

Success status precedes response fields. Errors below are produced by the handler or called HTTP upgrade/file-serving code; add the shared `401`/`403` above to permission-protected routes. `—` means no body or no explicit handler error. Most JSON errors are `{ok:false,error:string}`; authorization errors and phase-4 not-found responses have only `error`. Reports use plain-text HTTP errors. JSON timestamps are Unix epoch milliseconds unless a field explicitly uses seconds/hours; day/month strings retain their documented calendar format. `?` below marks an optional/omitted response field or optional input. Named records are expanded below the tables.

## Session

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/auth/me` | view | — | 200: `actor,name,role,local,permissions[]` | — |
| POST | `/api/auth/logout` | Public | — | 200: `ok:true`; clears cookie | — |

## Status / health

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/health` | Public | — | 200: `status:"ok",service:"EVA Station",version` | — |
| GET | `/api/info` | view | — | 200: `version,host,port,urls[],consoles,devices,startedAt` | — |
| GET | `/api/status` | view | — | 200: `version,uptimeS,schema,dbBytes,consoles,phones,lastBackup,pairing,https,serverTime` | — |
| GET | `/api/health/database` | view | — | 200: `ok,database:"SQLite",integrity,journalMode:"WAL",counts,checkedAt` | 503: DB unavailable; 500: integrity/query/check failure |

`/api/health` is public and returns no crew, mission, or medical data. It does not check SQLite. Database health flushes telemetry, runs `PRAGMA integrity_check`, and counts `crew,missions,telemetry,alerts,incidents,readiness,monthly_health,oplog`. Failure bodies contain `ok:false,database,error` (and `table` for count failures); a completed but unsuccessful integrity check uses the normal result shape with `ok:false`. Status `lastBackup` is a Backup or null, `https` is `{port,fingerprint}` or null, and `pairing` is `code` or `open (demo)`. A failed file stat leaves `dbBytes:0`; backup-list errors leave `lastBackup:null`.

## State & sync

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/state` | view | — | 200: `type:"snapshot",t,startedAt,mission,missions[],crew[],trends,openAlerts[],alerts[],incidents[],oplog[]` | —; snapshot ignores supporting query errors |
| GET | `/api/sync` | view | — | 200: array of `crewId,role,online,outboxPending,device?` | 500: crew-list failure |

Snapshot includes full clinical CrewView records, up to 12 missions, 60 recent alerts, 40 incidents, and 60 log entries. Trends are keyed by crew ID, with arrays `t,hr,spo2,temp,rr,ipv`. Unlike the console WebSocket snapshot, HTTP state has no `sim` or `info`. Sync chooses the newest non-revoked paired Device for each crew member; device/outbox query errors are ignored, and the listed device need not be the currently connected one.

## Missions

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| POST | `/api/mission/start` | command | `reason?` decoded but unused | 200: `ok:true,mission` | 409: not PLANNED or store failure |
| POST | `/api/mission/end` | command | `reason?` decoded but unused | 200: `ok:true,mission` | 409: not ACTIVE or store failure |
| POST | `/api/mission/abort` | command | `reason?` | 200: `ok:true,mission` | 409: not ACTIVE or store failure |
| POST | `/api/mission/new` | command | `reason?` decoded but unused | 200: `ok:true,mission` | 409: current ACTIVE/PLANNED or insertion failure |
| GET | `/api/missions` | view | — | 200: Mission array, at most 100 | 500 |
| POST | `/api/missions` | command | `id,name,environment,site,plannedH,crew:[{crewId,role}]`; `status,startedAt,endedAt` decoded but overwritten | 201: `mission,crew` (submitted assignments) | 400: decode, validation, or store error |
| GET | `/api/missions/{id}/crew` | view | — | 200: assignment array `crewId,role`, ordered by role | 500 |
| PUT | `/api/missions/{id}/crew` | command | Array of `{crewId,role}` | 200: submitted array | 404: mission lookup fails; 409: not PLANNED; 400: decode/unknown crew/assignment/store error |

`CreateMission` trims and uppercases ID, matching `^[A-Z0-9][A-Z0-9-]{2,31}$`; trims name to a required 1–80 Unicode characters; requires `0 < plannedH <= 12`, a nonempty assignment list, known crew, and at least one role beginning `EV`. Current mission must not be ACTIVE or PLANNED. Stored status is PLANNED and start/end timestamps are cleared. Environment/site have no explicit validation. Duplicate mission IDs fail insertion. Mission insertion and assignment replacement are separate transactions: assignment failure can leave an inserted mission even though the API returns 400.

`SetMissionCrew` allows only `EV1,EV2,EV3,IV`, rejects duplicate crew IDs, and replaces assignments transactionally. Multiple crew members may share a role. The PUT route permits an empty list and does not enforce an EV member; an unknown mission's GET assignment list is simply empty. Starting applies assigned roles; unassigned crew keep their existing roles. `/api/mission/new` creates the next preset mission using the trailing numeric ID/name convention and previous environment/site/duration; it does not accept a custom plan or copy assignments. Abort defaults an empty reason to `Operator decision` and opens incidents for EVA crew.

## Alerts & protocols

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/alerts` | view | —; query `mission,open,limit` | 200: Alert array | 500 |
| GET | `/api/alerts/{id}/responses` | view | — | 200: array `alertId,t,response,by,actor?` | 400: non-integer ID; 500: query error |
| POST | `/api/alerts/{id}/ack` | respond | `response?` (default `COPIED`) | 200: `ok:true,alert` | 400: non-integer ID; 404: invalid response or unknown alert |
| POST | `/api/instruction` | respond | `crewId?` (empty = all), `text` | 200: `ok:true,delivered` | 400: decode, empty text, unknown crew |
| POST | `/api/crew/{id}/incident` | respond | — | 200: `ok:true,incident` | 400: unknown crew / cannot open incident |
| POST | `/api/crew/{id}/help-clear` | respond | — | 200: `ok:true` (also for unknown crew/no help) | — |
| POST | `/api/incidents/{id}/seal` | respond | — | 200: `ok:true,incident` | 409: incident unknown or not recording (bad ID parses as 0) |
| GET | `/api/protocols` | view | — | 200: array `code,level,steps,source` | 500 |

Alert query: `mission` filters exact mission ID; empty means all. Only `open=1` filters unresolved alerts. `limit` accepts 1–1000, otherwise defaults to 200; results are newest first. Unknown alert IDs in response-history GET return an empty array. Acknowledgements accept only `COPIED,IM_OK,NEED_HELP`; HTTP records `by:"operator"` without a personal actor in alert-response history (the separate security audit still records the caller).

Instruction text is trimmed, required, and truncated to 140 Unicode characters. `delivered` is true for broadcasts or when the targeted crew has a hub connection; it is not a receipt acknowledgement. Durable replay is described in [WEBSOCKET.md](WEBSOCKET.md). Protocol `steps` is a **string containing a JSON array**, not an array; `level` is 1–3 and `source` is `builtin` or `medical`. No protocol write route exists.

## Crew records

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/crew` | medical | — | 200: array `{crew:Crew,contacts:Contact[]}` | 500: crew query; contact errors ignored |
| POST | `/api/crew` | command | Crew fields below | 201: saved Crew | 409: normalized ID already known; 400: decode/validation/store error |
| PUT | `/api/crew/{id}` | medical | Crew fields; body `id` replaced by normalized path ID | 200: saved Crew | 404: unknown crew; 400: decode/validation/store error |
| GET | `/api/crew/{id}/vitals` | medical | —; query `from,to` | 200: `crewId,from,to,rows:TelRow[]` | 404: unknown crew; 500: query failure |
| GET | `/api/crew/{id}/contacts` | medical | — | 200: Contact array | 404: unknown crew; 500 |
| PUT | `/api/crew/{id}/contacts` | medical | Array `{name,relation?,channel?,id?}`; input IDs ignored | 200: reread Contact array | 404: unknown crew; 400: decode/validation/store error |
| GET | `/api/crew/{id}/readiness` | medical | —; query `days` | 200: Readiness array | 500 |

Crew JSON fields: `id,name,initials,role,specialty,sex,age,heightCm,weightKg,bloodType,conditions,allergies,medication,radBudget,radAccum`. POST/PUT are full record writes, not patches. ID is trimmed/uppercased and must start `AST-`, length 5–12 bytes (there is no numeric-suffix check). `ValidateCrew` requires a nonblank name of at most 80 Unicode characters, role `EV1,EV2,EV3,IV`, sex `F,M`, age 18–80, height 0 or 120–230 cm, and weight 0 or 35–200 kg. Name is stored trimmed. Empty initials are derived from name words; nonpositive radiation budget defaults to 600. `radAccum` input is ignored: new records start at 0 and updates preserve accumulated history. No explicit checks constrain the remaining clinical strings.

Vitals: `from,to` are integer milliseconds; invalid/nonpositive `to` uses now. Invalid/nonpositive `from`, or `from >= to`, uses the preceding hour. Ranges exceeding 24 hours are clipped to the final 24 hours. The store is flushed; bounds are inclusive and rows ascend by time. TelRow fields: `t,hr,spo2,temp,rr,hrv,rad,effort,suitP,o2,co2,batt,ipv,status`; sensor values may be null. These rows do not expose `seq,eid,bpSys,bpDia`.

Contact replacement accepts at most 10 entries, requires every trimmed name to be nonempty, and generates new IDs in a transaction; `[]` clears contacts. Readiness `days` accepts 1–60, otherwise 14, and limits rows ordered by descending day rather than filtering a rolling time interval. Unknown crew returns an empty readiness array. Readiness fields are specified in [WEBSOCKET.md](WEBSOCKET.md).

## Sensors

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/sensors` | view | —; query `crew` (empty = all) | 200: Sensor array, ordered by crew/kind | 500 |
| POST | `/api/sensors` | command | `crewId,kind,interface,model?,profile?,status?,batteryPct?`; `id,lastSeen` also decode | 201: Sensor | 400: decode, unknown crew, constraint/store failure |
| PUT | `/api/sensors/{id}` | command | `status?,batteryPct?` | 200: `ok:true` | 404: missing sensor; 400: decode/constraint/store failure |

Sensor fields are `id,crewId,kind,interface,status,model?,profile?,batteryPct?,lastSeen?`. Creation overwrites ID, defaults empty status to `registered`, and does not persist supplied `lastSeen` (although it is echoed). Update preserves status on empty string and battery on null/omission, always marking `lastSeen` as now; it cannot clear battery to null.

| SQLite CHECK | Accepted values |
| --- | --- |
| kind | `hr,hrv,spo2,temp,rr,bp,glucose,scale,dosimeter,suit,other` |
| interface | `ble-gatt,manual,suit-bus,simulated` |
| status | `registered,connected,disconnected,retired` |
| batteryPct | null or 0–100 inclusive |

`crewId` references crew. Profile/model are not validated against a catalogue. Sensor registration does not define a sensor transport protocol.

## Simulator

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/sim` | view | — | 200: `scenarios[],virtual,active` | — |
| POST | `/api/sim/inject` | command | `crewId,scenario` | 200: `ok:true,target` (`phone` or `virtual suit`) | 400: decode, no phone/suit, unknown virtual scenario |
| POST | `/api/sim/clear` | command | `crewId?` (empty = all) | 200: `ok:true` | — |
| POST | `/api/sim/virtual` | command | `crewId,on` | 200: `ok:true` | 409: enabling while phone linked; 400: decode/no registered virtual suit |

Scenario fields: `id,label,description,metric`. IDs: `tachycardia,hypoxia,overheat,leak,co2,battery,radiation,sensor,signal`. `virtual` maps crew IDs to enabled booleans; `active` maps IDs to scenario lists (possibly null). Injection prefers a linked phone and forwards its scenario without server validation; otherwise it enables the existing virtual suit before validating/injecting. Clear tolerates unknown crew. Virtual suits are created for EVA crew at simulator initialization; the endpoint checks existence, not just the current crew role.

## Black box / exports / reports

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/blackbox` | view | —; query `crew,kind,from,to,limit` | 200: ChainEntry array | 500 |
| GET | `/api/blackbox/verify` | view | — | 200: `ok,entries,brokenAt?,lastHash,checked` | 500: query/check execution error |
| GET | `/api/export/{table}` | medical | — | 200: CSV with UTF-8 BOM, header row, attachment filename | 404: unknown table or export error |
| GET | `/reports/mission/{id}` | medical | — | 200: printable mission HTML | 404: report lookup; 500: template execution attempt |
| GET | `/reports/incident/{id}` | medical | — | 200: printable incident HTML | 404: lookup (bad integer becomes 0); 500: template execution attempt |
| GET | `/reports/monthly/{crew}` | medical | —; query `month` | 200: printable monthly clinical HTML | 404: unknown crew; 500: template execution attempt |

Black-box filters use exact crew/kind, inclusive millisecond time bounds, and descending ID. `limit` accepts 1–1000, otherwise 150. Unparseable times become 0; absent/nonpositive `to` has no practical upper bound when filtering. With no crew/kind and no positive bound, the handler uses the unfiltered listing. ChainEntry fields: `id,t,kind,crewId?,ref?,payload,prevHash,hash`; payload is a JSON-encoded string. Chain verification can return HTTP 200 with `ok:false` for a broken chain.

Export whitelist: `telemetry,alerts,incidents,readiness,oplog,missions,crew,eva_summary,monthly_health,blackbox_chain`. An optional `.csv` suffix is removed. Exports cover the full selected query, with SQL column names; there are no date/crew filter parameters. Filename: `eva-<table>-YYYY-MM-DD.csv`. Null cells are empty.

Monthly `month` convention is `YYYY-MM`; omission selects the latest stored month, or the current month if none exists. Malformed or missing-data months are not rejected by the report builder and can render without current-panel data. Reports set HTML content type and `Cache-Control:no-cache`. A template error after output begins may leave HTTP 200 with partial HTML despite the attempted 500.

## Administration: users

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/users` | admin | — | 200: User array | 500 |
| POST | `/api/users` | admin | `name,role` | 201: `user,token,link` (`/?token=...`) | 400: decode, name/role, store error |
| POST | `/api/users/{id}/disable` | admin | — | 200: `ok:true` | 404: missing/already disabled user or store error |

User fields: `id,name,role,createdAt,createdBy,disabled`. Name is trimmed and must contain 1–80 Unicode characters; role must exist in `roles`. Creation is the only response exposing the new token. Listing includes disabled users without tokens; disabling invalidates subsequent personal-token authentication.

## Administration: devices / pairing

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/devices` | admin | — | 200: Device array, newest paired first | 500 |
| POST | `/api/devices/pair` | admin | `crewId` | 201: `code,crewId,expiresAt` | 400: decode/unknown crew |
| POST | `/api/devices/{id}/revoke` | admin | — | 200: `ok:true,crewId` | 404: missing/already revoked device or store error |

Device fields: `id,crewId,name,pairedAt,pairedBy,lastSeen?,lastSeq?,revoked`. Tokens are never listed. Pairing returns a six-digit string, single-use, valid five minutes, held in memory; 20 failed code attempts across the pairing book cancel all pending codes. Revoke attempts to notify/disconnect the connected phone with that device ID. Pairing and reconnect details are in [WEBSOCKET.md](WEBSOCKET.md).

## Administration: backups

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/backups` | admin | — | 200: Backup array | 500 |
| POST | `/api/backups` | admin | — | 201: Backup | 500: snapshot/verification failure |

Backup fields: `name,bytes,createdAt,verified,entries`; filesystem path is omitted. Creation flushes telemetry, uses SQLite `VACUUM INTO`, and verifies integrity and the hash chain. Listing does not verify contents (`verified`/`entries` are not fresh verification results). No HTTP restore/download/delete route is registered.

## Administration: audit

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/api/audit` | admin | —; query `limit` | 200: array `id,t,actor,role,ip?,action,target?,result` | 500 |

`limit` accepts 1–1000, otherwise 200; newest IDs first. The read itself is audited after the response is generated.

## WebSocket upgrades and static routes

| Method | Path | Permission | Request body (JSON fields) | Response (main fields) | Errors (status codes produced by handler) |
| --- | --- | --- | --- | --- | --- |
| GET | `/ws/device` | Public HTTP upgrade; paired-device hello required | — | 101; phone protocol | 400: invalid upgrade/missing key; 426: version not 13; 500: hijacking unsupported |
| GET | `/ws/console` | view; origin guard on upgrade | — | 101; console protocol | 400, 426, 500 as above |
| GET | `/app/` | Public | — | Phone static files | FileServer errors, including 404 for missing files |
| GET | `/shared/` | Public | — | Shared static files | FileServer errors, including 404 |
| GET | `/app` | Public | — | 302 to `/app/` | — |
| GET | `/` | `operatorPage`: authenticated view | —; query `operator` or `token` for login | Console files, or 303 login redirect | 401: unavailable view access; FileServer errors, including 404 |

These are the exact registered patterns; slash-ending static patterns cover subpaths, including the console catch-all `/`. Static responses use `Cache-Control:no-cache`. The Go mux supplies method matching (GET patterns also match HEAD) and automatic routing responses; those are separate from handler errors. Every response through the top-level middleware receives `X-Content-Type-Options:nosniff`, `Referrer-Policy:no-referrer`, and `X-Frame-Options:DENY` (the manually written WebSocket 101 handshake does not copy those headers).

## Shared response records

| Record | Main JSON fields |
| --- | --- |
| Mission | `id,name,environment,site,plannedH,status,startedAt?,endedAt?`; statuses `PLANNED,ACTIVE,COMPLETED,ABORTED` |
| Alert | `id,missionId?,crewId,t,level,code,metric?,voice,reason,protocol[],response,respondedBy?,respondedAt?,responseS?,escalated,escalatedAt?,resolvedAt?` |
| Incident | `id,missionId?,crewId,alertId?,trigger,eventT,windowStart,windowEnd,status,classification?,firstDeviation?,detectionS?,responseS?,samplesCount,sha256?,sealedAt?` |
| OpLog | `id,missionId?,t,actor,crewId?,action,detail` |
| CrewView | All Crew fields plus `eva,baseline,eval,vitals?,factors[],vuln,dose,device,readiness?,lastEva?,help`; console shapes expanded in [WEBSOCKET.md](WEBSOCKET.md) |

## Contract limitations visible in code

Success is not uniformly a persistence guarantee: acknowledgement/history writes ignore database errors, `SealNow` does not propagate a sealing write error, and several list/snapshot paths ignore supporting query failures. The public response does not distinguish these failures. Mission creation is not atomic with crew assignment. Body-size enforcement and ignored JSON errors are described above. Instruction `delivered` is a connection-state hint, not phone confirmation. These are implementation behaviors, not stronger guarantees inferred from comments.

## curl examples

Replace `STATION:PORT` with the actual station address. `TOKEN` is a placeholder for an authorized console token. These commands omit browser origin headers, which the guard permits.

```sh
curl 'http://STATION:PORT/api/health'
curl -H 'Authorization: Bearer TOKEN' 'http://STATION:PORT/api/auth/me'
curl -H 'Authorization: Bearer TOKEN' 'http://STATION:PORT/api/alerts?open=1&limit=20'
curl -H 'Authorization: Bearer TOKEN' -H 'Content-Type: application/json' \
  -d '{"crewId":"AST-001","text":"Return to the airlock."}' 'http://STATION:PORT/api/instruction'
curl -c cookies.txt 'http://STATION:PORT/?token=TOKEN'
curl -b cookies.txt 'http://STATION:PORT/api/state'
```
