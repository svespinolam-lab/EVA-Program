EVA Station uses JSON WebSocket messages for authenticated phone telemetry/control at `/ws/device` and the authorized live console feed at `/ws/console`. Phone readings are buffered locally and acknowledged by sequence after a store transaction; selected station messages have a separate durable outbox and receipt acknowledgement. This document describes the wire shapes, actual client behavior, and delivery limits visible in the implementation.

Source of truth: `internal/server/server.go`, `internal/server/auth.go`, `internal/server/pairing.go`, `internal/server/outbox.go`, `internal/server/hub.go`, `internal/server/api.go`, `internal/ws/ws.go`, `internal/engine/engine.go`, `internal/engine/blackbox.go`, `internal/engine/rules.go`, `internal/model/model.go`, `internal/store/store.go`, `internal/store/access.go`, `internal/store/schema.sql`, `internal/store/migrate.go`, `internal/sim/sim.go`, `web/app/js/engine/link.js`, `cmd/eva-station/main.go`.

## Transport and admission

Both paths require a WebSocket version-13 upgrade. Upgrade errors are HTTP 400 (missing upgrade/key), 426 (wrong version), or 500 (hijacking unsupported). The transport accepts complete text or binary payloads for JSON parsing, supports fragmentation, and caps messages at 8 MiB. Server application messages are text JSON. No subprotocol is negotiated. All numeric timestamps are Unix epoch milliseconds; `seq`, `alertId`, and `msgId` are integers. `?` in this document marks optional/omitted fields, not a literal key suffix.

| Connection | Authorization | Initial output / input | Read deadline |
| --- | --- | --- | --- |
| Phone `/ws/device` | No HTTP console permission or origin check; identity established by hello | First application message must be `hello` within 15 s | 60 s reset before each subsequent `ReadMessage` |
| Console `/ws/console` | `view` permission and same-origin upgrade guard from `auth.go` | Server immediately sends `snapshot`; no hello required | 90 s reset before each `ReadMessage` |

Console authorization accepts the station PC, bootstrap token, or a permitted personal token through cookie/Bearer authentication, as described in [API.md](API.md). It is checked at upgrade, not on every later message. Phone token authentication is checked at hello; explicit revocation also closes an active matching device.

Each client has a 512-message outgoing queue. A full queue drops the newly submitted message without blocking the engine; `Hub.Device` still reports true if a client exists. A writer sends WebSocket control pings every 20 s and uses a 10 s write deadline. Control pongs are consumed inside `ReadMessage` and **do not reset the application read deadline**. A quiet console therefore needs application traffic within 90 s even if it answers control pings. Phone `link.js` sends `queue` every 10 s; it does not schedule JSON `ping` messages.

Malformed first JSON/non-hello gets `bad_hello` and closes. After authentication, malformed JSON and unknown message types are silently ignored. Payload-specific unmarshal failures for telemetry/monthly/readiness are also ignored. A second hello has no handler. A new connection for the same crew replaces the previous connection: `replaced` is queued, then the old connection is closed after 300 ms. `device_revoked` is similarly queued before a 300 ms delayed close; neither notice is guaranteed to survive queue/network failure.

## Pairing and hello

`hello` has `type:"hello",crewId?,device?,deviceId?,token?,pairCode?`. Device label is trimmed, defaults to `Phone`, and is truncated to 60 bytes by `wsDevice` (the store additionally limits it to 60 Unicode characters). Authentication uses this precedence:

| Variant | Required fields | Result |
| --- | --- | --- |
| Reconnect | Nonempty `deviceId` and `token`; optional `crewId` | Token hash must match an unrevoked device. If supplied, crew ID must match its assignment. No new `paired` message. |
| One-time code | Nonblank trimmed `pairCode`; optional `crewId` | Code determines crew. Supplied crew must match. Consumes code before creating device; returns credentials in `paired`. |
| Open pairing | `crewId` known to engine, with server `OpenPairing` enabled | Creates device without code; emits `paired`. Demo-only option. |
| No valid variant | Known crew without credentials/code, or unknown crew | `pairing_required` or `unknown_crew`, then close. |

An invalid complete credential pair does not fall back to a supplied code. An incomplete pair falls through to code/open-pairing checks. Codes come from admin `POST /api/devices/pair {crewId}` and are six-digit strings, valid five minutes, single-use, stored only in memory. Expiry cleanup removes codes when current time is greater than expiry. Failed/mismatched code attempts share one counter; the 20th failure clears every pending code and resets that counter. Successful consumption does not reset it. Pairing a new device does not revoke prior device records, though only one phone per crew can be connected.

On authentication, the station registers the connection, touches device last-seen, marks the crew online (disabling its virtual suit), then queues `paired` if newly paired, `welcome`, and pending durable messages in oldest-ID order. Live broadcasts may interleave because registration happens before these sends.

```text
Console                 Station                         Phone
   | POST /api/devices/pair {crewId}                         |
   |---------------------->|                               |
   |<-- 201 {code,crewId,expiresAt}                          |
   |       user enters code on phone --------------------->|
   |                       |<-- WS upgrade /ws/device ------|
   |                       |<-- hello {pairCode,crewId,device}
   |                       | consume code; create device   |
   |                       |--- paired {deviceId,token,crewId} -->
   |                       |--- welcome ------------------>|
   |                       |--- pending outbox messages -->|
   |                       |       phone stores linkDevice |
```

## Phone → station messages

Every row includes a string `type` with the listed value. After hello, server identity comes from the authenticated connection, not a payload's claimed crew ID.

| Type | Other JSON fields | Handling / response |
| --- | --- | --- |
| `hello` | `crewId?,device?,deviceId?,token?,pairCode?` | First message only; variants above. |
| `telemetry` | Sample fields below | Forces crew ID and computed `eid`; passes to `Ingest`. No immediate per-reading response. |
| `batch` | `items:Sample[]` | Forces authenticated crew in ingestion, computes each `eid`, sorts by timestamp; returns `batch-ok {n}`. No explicit server item-count cap. |
| `queue` | `n` | Sets displayed queued count verbatim (no clamp); no reply. |
| `monthly-health` | Monthly fields below | Forces crew ID; upserts panel. Success: `monthly-health-ok {month}` and console `oplog`; failure: uncoded `error`. |
| `readiness` | Readiness fields below | Forces crew ID; stores/evaluates daily verdict. Success: `readiness-ok {day}`; failure: uncoded `error`. |
| `ack` | `alertId,response` | Only own crew's alerts; `COPIED,IM_OK,NEED_HELP`. Success normally emits `alert-update`; error for invalid response/unknown or foreign alert. |
| `msg-ack` | `msgId` | Marks an undelivered outbox row delivered only if owned by authenticated crew. No response; duplicate/unknown/foreign IDs have no effect. |
| `incident` | None | Opens manual incident for authenticated crew; console receives incident/log updates. Return error is ignored; no direct phone confirmation. |
| `ping` | None | `pong {t}`; distinct from WebSocket control ping/pong. |

### Input record shapes

| Record | Fields / actual validation |
| --- | --- |
| Sample | `crewId,t,missionId?,seq?,hr?,spo2?,temp?,rr?,hrv?,rad?,effort?,suitP?,o2?,co2?,batt?,bpSys?,bpDia?,cal?,source?,eid?`. Sensor fields are nullable numbers; `effort` is nullable integer (0 rest, 1 moderate, 2 high). `cal` is `{temp?,hr?}` numeric offsets. Server overwrites crew/`eid` and derives stored mission from station state/time. Accepted source tags are `device,virtual,replay`; otherwise fallback is `device`. |
| Monthly | `crewId,month,avgHr,avgSpo2,avgSleepH,avgHrv,hemoglobin,wbc,platelets,glucose,orthostaticDrop,weightKg,weightChangePct,exercisePct,radiationMonth,mentalIndex`. Values except crew/month are nullable; `mentalIndex` is integer, the others numeric. Omission becomes null. Requires parseable `YYYY-MM`. Saves only non-psychological fields, merges non-null values into existing month, ignores submitted `mentalIndex` (new rows store null; existing mental value is not updated). No numeric range validation here. |
| Readiness | `crewId,day,status,vulnerability,summary,factors:string[],sleepH?,hydration?,fatigueHigh,hrRest?,hrvRest?,receivedAt`. `status` must be `GREEN,YELLOW,RED`; invalid `day` is replaced with station today; vulnerability clamped 0–100, summary truncated to 200 bytes, receivedAt overwritten. Hydration is nullable integer; sleep/rest measures nullable numbers. Other fields have no explicit range checks here. |

Sample units: `hr` bpm, `spo2` %, `temp` °C, `rr` breaths/min, `hrv` RMSSD ms, `rad` mSv/h, `suitP` psi, `o2` remaining minutes, `co2` mmHg, `batt` %, and `bpSys,bpDia` mmHg. Missing sensor values mean unavailable, not zero.

`link.js` sends only finite readings from its own EVA crew, with `seq`, client `Date.now()` as `t`, and the fields `hr,spo2,temp,rr,hrv,rad,effort,suitP,o2,co2,batt,bpSys,bpDia,source`. It does not send `cal`, `missionId`, or `eid`. Its readiness sender sends `day,status,vulnerability,summary,factors,sleepH,hydration,fatigueHigh`, derived from a current check-in; raw mood, energy, notes, and diary entries are not sent. Its monthly sender permits only finite `hemoglobin,wbc,platelets,glucose,orthostaticDrop,weightKg,radiationMonth`, requires at least one such value and a `YYYY-MM` month, and maintains a separate per-month local outbox.

## Station → phone messages

| Type | Other JSON fields | Recipient / client behavior |
| --- | --- | --- |
| `paired` | `deviceId,token,crewId` | Newly paired phone saves credentials as `linkDevice`; clears entered code/pairing-needed flag. Token is returned only at pairing. |
| `welcome` | `crewId,serverTime,mission,crew[],alerts` | Public roster below; alerts are own open RISK/EMERGENCY alerts (may be null). Phone marks online, resets retry delay, applies mission/crew/alerts, sends readiness/queue, starts telemetry and monthly replay. |
| `crew` | `t,mission,crew[]` | Every engine tick (1 Hz); crew entries below. Phone applies roles for locally known members and ingests other members' vitals. |
| `mission` | `mission` | Mission transition; phone stores station mission and ACTIVE start time. |
| `alert` | `alert:Alert` | New/escalated-level alert for affected crew, sent to phone only at RISK/EMERGENCY. Phone shows pending unresolved own alerts and deduplicates voice by ID/level in memory. |
| `alert-update` | `alert:Alert` | Resolution/acknowledgement to affected crew. Phone removes resolved/nonpending alerts. Timeout escalation updates consoles; it is not separately sent to the affected phone in that branch. |
| `buddy` | `alertId,crewId,role,name,voice,reason,level:3,msgId?` | Other crew, after NEED_HELP or unacknowledged RISK/EMERGENCY for 30 s; banner/voice. Durable for 10 min. |
| `instruction` | `t,text,from:"EVA Station",crewId,msgId?` | Target or all crew (`crewId:""`); banner/voice. Text trimmed and truncated to 140 Unicode characters. Durable for 6 h. |
| `abort` | `missionId,reason,voice,msgId?` | All crew; abort overlay/voice. Durable for 6 h. Phone repeats voice every 20 s until local Copy; Copy is not an application acknowledgement. |
| `ack-seq` | `seq` | Positive maximum sequence for that crew in a committed writer batch; phone prunes sent readings through it. |
| `batch-ok` | `n` | Number of accepted batch items, not new DB rows or a commit guarantee. Phone advances replay if count matches in-flight length. |
| `monthly-health-ok` | `month` | Removes matching monthly item only if unchanged since send, then sends the next. |
| `readiness-ok` | `day` | Echoes input day (even if engine replaced an invalid day); phone makes no state change. |
| `pong` | `t` | Response to JSON ping; phone makes no state change. |
| `inject` | `scenario` | Simulator request; phone calls local telemetry injection. |
| `clear` | None | Phone clears its local scenarios. |
| `replaced` | `reason` | Another phone connected as this crew; phone disables link. |
| `error` | `error`, optional `code` | Pairing errors enter unpaired state as below; other errors show a toast and clear monthly in-flight bookkeeping. |

Public welcome roster entries contain **only** `id,name,initials,role,specialty`. The 1 Hz crew entries contain **only** `id,role,name,level,vitals,lastT,online`; `vitals` is the latest Sample or null. Neither roster nor crew broadcast carries IPV, diagnosis, readiness factors, or full clinical crew records. This privacy rule does not mean every phone message is diagnosis-free: its own Alert contains `reason/protocol`, and a buddy relay can contain the originating alert's `reason`. `vitals` uses the full Sample shape, including calibration offsets if a client supplied them, not the reason for those offsets.

| Error code | When sent | `link.js` reaction |
| --- | --- | --- |
| `bad_hello` | First payload invalid JSON or not hello | Generic error handling; no dedicated unpaired transition. |
| `pairing_required` | Known crew, no accepted credentials/code, open pairing off | Waits unpaired; leaves stored credentials intact. |
| `pairing_failed` | Invalid/expired/crew-mismatched code, or device creation failure | Clears stored credentials; waits unpaired. |
| `device_unknown` | Wrong device ID/token, missing device, or revoked credential at hello | Clears credentials; waits unpaired. |
| `crew_mismatch` | Token's crew differs from nonempty hello crew | Clears credentials; waits unpaired. |
| `unknown_crew` | No credential/code branch succeeded and hello crew is unknown | Generic error handling; no dedicated unpaired transition. |
| `device_revoked` | Admin revokes connected matching device | Clears credentials; waits unpaired. |
| No code | Monthly/readiness/ack error; marshal fallback `error:"encode"` | Generic error handling. |

The five recognized pairing errors clear the pending code and stop automatic retry scheduling until pairing is requested. Normal reconnect backoff starts at 1 s and doubles to 10 s, resetting on welcome. Local `unpair()` deletes credentials and closes the link; it does not revoke the server device record. Credentials are used only when stored crew matches selected crew; if matching credentials remain, they take precedence over a newly entered pair code.

## Store-and-forward and idempotency

`link.js` persists a monotonically increasing `linkSeq` and an `outbox` of readings before sending. It retains at most 20,000 readings, dropping oldest beyond that cap. On welcome, it selects queued readings for the current crew and sends one batch of up to 300 at a time. Successfully submitted sequences are tracked in an in-memory `sent` set. It closes/reconnects if `batch-ok` does not arrive within 15 s or its count differs from the batch size. Live telemetry can continue while replay is in progress.

`batch-ok` only advances the replay cursor. `ack-seq` prunes entries for the current crew with `seq <= acknowledgement` **only if present in `sent`**, so a higher live acknowledgement does not discard older readings not yet sent. It also removes acknowledged numbers from `sent`, persists the queue, and advances displayed `lastAck` monotonically. Unacknowledged rows remain for the next connection. Monthly records are removed by `monthly-health-ok`, not `ack-seq`.

```text
Phone                         Station engine                 SQLite writer
  | persist reading {seq,t,...}     |                               |
  |--- telemetry {seq,t,...} ------>|--- queue evaluated row ------->|
  |                                |                commit transaction
  |<---------------- ack-seq {seq} ---------------------------------|
  | prune only rows sent this connection with seq <= ack             |
  | [offline: readings accumulate in local outbox]                  |
  |--- reconnect hello ----------->|                               |
  |<-- welcome --------------------|                               |
  |--- readiness; queue {n} ------->|                               |
  |--- batch {items: up to 300} --->|--- live/backfill rows --------->|
  |<-- batch-ok {n} ----------------|                               |
  | send next batch                 |                commit transaction
  |<---------------- ack-seq {seq} ---------------------------------|
  | prune acknowledged sent rows   |                               |
```

The diagram shows a typical order; commit acknowledgements and `batch-ok` can interleave. Writer flushes every 250 ms, at 500 buffered rows, or on explicit Flush. The station's startup wiring attaches `Store.OnCommit` to `Server.OnCommit`.

| Rule | Implementation |
| --- | --- |
| Timestamp identity | Unique index on `(crew_id, ts)` (`UNIQUE(crew_id, ts)` semantics); inserts use `INSERT OR IGNORE`. Replays do not overwrite stored rows. |
| Sequence identity | Server computes `event_id = "<deviceId>:<seq>"` for positive seq; otherwise empty → SQL null. Migration adds a unique non-null `event_id` index. Client `eid` is overwritten. |
| Live timestamp normalization | A single reading with `t <= 0` or `t > now+5000` gets station now. A reading at/before the latest timestamp or over 15 s old goes through backfill. |
| Batch timestamps | Sort ascending; skip `t <= 0` or `t > now+5000`. A reading within 15 s and newer than latest is live; others backfill. `n` counts accepted items including duplicates. |
| Backfill | Stateless evaluation/storage using mission at sample time; does not replace live state or raise live alerts. A batch with emergency backfill can open a historical incident at its first emergency timestamp. |
| Receipt scope | `ack-seq` is a maximum per **crew**, routed to the currently connected phone; it is not a per-device contiguous sequence watermark. The current device record is touched with that sequence. |

These constraints provide storage deduplication, not exactly-once application effects. The writer updates its maximum sequence even if an individual insert errors or is ignored; after a successful transaction commit it can acknowledge that maximum. Thus `ack-seq` does not prove that every individual row was inserted. Transaction failure emits no acknowledgement and clears the writer buffer; the phone's retained outbox is the retry source. No phone-side timer waits specifically for `ack-seq` after a matching `batch-ok`.

## Durable station outbox

Only `instruction` (6 h), `abort` (6 h), and `buddy` (10 min) are durable through the configured emitter. They are stored per crew, stamped with `msgId`, and attempted live. Broadcasts create one row per crew except the excluded crew, including currently offline/unpaired crew and IV. Other message types use the transient hub queue. Pending replay selects rows with no delivered timestamp and `expires_at > now`, ordered by ID, after welcome; there is no periodic replay while the same connection stays open.

```text
Operator/API              Station / SQLite                    Phone
   | instruction for crew       |                           [offline]
   |--------------------------->| insert outbox, stamp msgId     |
   |<-- ok, delivered:false -----|                               |
   |                            |<-- reconnect hello ------------|
   |                            |--- welcome ------------------->|
   |                            |--- instruction {...,msgId} --->|
   |                            |<-- msg-ack {msgId} -------------|
   |                            | mark delivered for this crew   |
   |                            |       phone dedupes / displays |
   |                  [if ack lost: replay at next reconnect]   |
   |                            |--- same msgId ---------------->|
   |                            |<-- msg-ack {msgId} -------------|
   |                            |       repeat suppressed locally|
```

The phone sends `msg-ack` **before** duplicate detection and UI handling. Its persisted `linkMsgs` keeps at most 300 keys, each `msgId + ':' + (t || alertId || missionId || '')`; a remembered key suppresses repeated effects but is acknowledged again. This is receipt/dedupe bookkeeping, not evidence a person read or acted on the message. Old keys can be evicted, and an error after recording the key can suppress a future replay's UI effect.

Outbox insertion failure falls back to live delivery without `msgId`. Insertion initially writes `{}` and payload replacement is a separate update whose errors are ignored; durability and final payload replacement are not atomic. Pending-query errors silently skip replay. Expired rows are excluded, not delivered; `msg-ack` ownership checks crew, not the originating paired device. Queue saturation can drop a durable live/replayed send, leaving it pending until another reconnect or expiry. The code therefore does not establish lossless or exactly-once delivery.

## Console protocol

`wsConsole` sends a snapshot before adding the connection to the hub. Changes during that interval can be missed until later updates. Inbound application messages are read and discarded: there are no console command cases, no JSON parsing, and no application pong response. Actions use HTTP APIs. Any complete incoming application message renews the next 90 s read deadline.

| Direction | Type | Other JSON fields / meaning |
| --- | --- | --- |
| Console → station | Any application payload | Ignored; keeps read loop moving. |
| Station → console | `snapshot` | `t,startedAt,mission,missions,crew,trends,openAlerts,alerts,incidents,oplog,sim,info`; initial full state. |
| Station → console | `tick` | `t,mission,crew`; full clinical CrewView array at 1 Hz. |
| Station → console | `alert` | `alert:Alert`; raised or level-updated alert, including advisories. |
| Station → console | `alert-update` | `alert:Alert`; acknowledgement, resolution, or timeout escalation. |
| Station → console | `oplog` | `entry:OpLog`; engine actions and monthly-health sync log. |
| Station → console | `incident` | `incident:Incident`; opened/sealed incident. |
| Station → console | `mission` | `mission:Mission`; mission transition. |
| Station → console | `readiness` | `crewId,readiness:Readiness`; normalized saved verdict. |
| Station → console | `devices` | No other fields; pairing/revocation invalidation notice. Not sent for every connection/disconnection. |
| Station → console | `sim` | `sim:{scenarios,virtual,active}`; simulator HTTP actions. |
| Station → console | `error` | `error:"encode"` only as hub marshal-failure fallback. |

Instructions/aborts/buddy messages are sent to phones; the console receives associated mission/log/alert state rather than those direct message types. There is no console `crew` message: its regular message is `tick`.

### Nested console and alert shapes

| Record | JSON fields |
| --- | --- |
| Mission | `id,name,environment,site,plannedH,status,startedAt?,endedAt?` |
| Alert | `id,missionId?,crewId,t,level,code,metric?,voice,reason,protocol:string[],response,respondedBy?,respondedAt?,responseS?,escalated,escalatedAt?,resolvedAt?`; levels `ADVISORY,RISK,EMERGENCY`; response also permits `PENDING,NO_RESPONSE` in stored state. |
| CrewView | Crew fields `id,name,initials,role,specialty,sex,age,heightCm,weightKg,bloodType,conditions,allergies,medication,radBudget,radAccum`, plus `eva,baseline,eval,vitals?,factors[],vuln,dose,device,readiness?,lastEva?,help`. |
| Baseline | `crewId,phase,days,hrRest,hrRestSd,hrv,hrvSd,spo2,spo2Sd,temp,tempSd,rr,rrSd,bpSys,bpDia,sleepH,updatedAt` |
| Evaluation | `ipv,status,level,findings[],diagnosis,protocol[],voice?,updated`; status `STABLE,VULNERABLE,RISK,EMERGENCY,NO_DATA`; level -1 or 0–3. |
| Finding / Factor | Finding: `code,level,metric?,label,value?,since,fault?`. Factor: `code,label,points`. |
| DeviceView | `online,name?,since?,source,data?,lastT,queued,virtual`; source `device,virtual,none` is link/provider, data is latest reading provenance. |
| lastEva (EVASummary) | `missionId,crewId,durationS,samples,hrMean,hrMax,spo2Min,tempMax,trimp,kcal,doseMsv,peakIpv,alerts,riskS,recoveryH,endedAt` |
| Incident | `id,missionId?,crewId,alertId?,trigger,eventT,windowStart,windowEnd,status,classification?,firstDeviation?,detectionS?,responseS?,samplesCount,sha256?,sealedAt?` |
| OpLog | `id,missionId?,t,actor,crewId?,action,detail` |
| Trends | Map by crew ID; each value has arrays `t,hr,spo2,temp,rr,ipv` (15-minute history sampled into 5 s buckets). |
| sim | `scenarios:[{id,label,description,metric}],virtual:{crewId:boolean},active:{crewId:scenarioIds[] or null}` |
| info | `version,host,port,urls[],consoles,devices,startedAt` |

Snapshot includes up to 12 missions, 60 recent alerts, 40 incidents, and 60 log entries, plus all in-memory open alerts. Supporting query errors are ignored. CrewView sorting prioritizes EVA members, then members with data, then higher IPV. Clinical fields are present in both snapshot and tick; phone roster privacy rules do not apply to consoles.

## Known contract ambiguities and limits

The code does not guarantee that a committed maximum sequence represents every persisted row, that `msg-ack` means successful display, or that a connected phone received an instruction. Queue drops and non-atomic outbox payload replacement further limit delivery guarantees. `readiness-ok.day` echoes the pre-normalization day. Welcome/initial snapshot registration order allows live-message interleaving or gaps. These behaviors are documented as implemented; no stronger acknowledgement, ordering, or recovery semantics are specified by the code.
